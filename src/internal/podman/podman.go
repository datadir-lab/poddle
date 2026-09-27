// Package podman implements the sandbox provider backed by Podman, local or
// remote. A remote host is addressed via Podman's --url ssh://... transport, so
// the same code lists/creates/execs sandboxes regardless of where they run.
package podman

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
)

// lockNetPrefix names a pod's internal lock network ("poddle-lock-<pod>"): the
// pod's only route out, which the broker is attached to for that pod's lifetime.
const lockNetPrefix = "poddle-lock-"

// The containerized broker's pod-facing listener ports. Fixed (not ephemeral)
// so a broker restart does not strand pods that baked the old port into their
// env — see EnsureBroker. They live in the broker's own network namespace.
const (
	brokerGatewayPort  = "7440" // injecting HTTP gateway
	brokerForwardPort  = "7441" // egress forward proxy (HTTP_PROXY)
	brokerRedisPort    = "7442" // L4 Redis
	brokerPostgresPort = "7443" // L4 Postgres
)

// Provider talks to a Podman engine. Conn is empty for local, or an ssh URL
// (e.g. "ssh://user@host/run/user/1000/podman/podman.sock") for a remote host.
type Provider struct {
	Runner exec.Runner
	Conn   string
}

// New returns a Provider using the given runner and connection (empty = local).
func New(r exec.Runner, conn string) *Provider {
	return &Provider{Runner: r, Conn: conn}
}

// podman prepends the connection URL (if any) before the given args.
func (p *Provider) podman(args ...string) []string {
	if p.Conn != "" {
		return append([]string{"--url", p.Conn}, args...)
	}
	return args
}

// List returns every poddle-managed sandbox on the target engine, any state.
func (p *Provider) List() ([]sandbox.Sandbox, error) {
	args := p.podman("ps", "-a",
		"--filter", "label=poddle.managed=true",
		"--format", "json")
	res, err := p.Runner.Run("podman", args...)
	if err != nil {
		return nil, fmt.Errorf("podman ps: %w: %s", err, res.Stderr)
	}
	return parseList(res.Stdout)
}

// Create starts a detached container for the spec and returns its id. It runs
// `tail -f /dev/null` (portable across busybox/coreutils) to stay alive so it
// can be attached to.
func (p *Provider) Create(s sandbox.Spec) (string, error) {
	netName := ""
	if s.Network != nil {
		// The lock network is pre-created by the up-flow (EnsurePodLockNetwork)
		// before Create runs, so the broker can connect to it and read its IP.
		netName = lockNetPrefix + s.Name
	}

	args := p.podman("run", "-d",
		"--name", s.Name,
		"--label", "poddle.managed=true",
		"--label", "poddle.name="+s.Name,
		"--label", "poddle.template="+s.Template,
		"--label", "poddle.runtime="+s.Runtime,
		"--label", "poddle.size="+s.Size,
		"--label", "poddle.repo="+s.Repo,
		"--label", "poddle.mode="+s.Mode,
		"--label", fmt.Sprintf("poddle.autoscale=%t", s.Autoscale),
		"--label", "poddle.image="+s.Image,
		"--label", "poddle.identity="+s.Identity,
		"--label", "poddle.harness="+s.Harness,
		"--label", "poddle.policy="+s.PolicyName,
	)
	if netName != "" {
		args = append(args, "--network", netName)
	}
	if s.CPUs > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%g", s.CPUs))
	}
	if s.Memory != "" {
		args = append(args, "--memory", s.Memory)
	}
	for _, m := range s.Mounts {
		v := m.Host + ":" + m.Container
		if m.ReadOnly {
			v += ":ro"
		}
		args = append(args, "--volume", v)
	}
	for _, vol := range s.Volumes {
		args = append(args, "--volume", vol.Name+":"+vol.Container) // named volume, auto-created
	}
	for _, k := range sortedKeys(s.Env) {
		args = append(args, "--env", k+"="+s.Env[k])
	}
	args = append(args, s.Image, "tail", "-f", "/dev/null")

	// Pre-create named volumes with a pod label so `down` can find + remove them.
	// Best-effort — an already-existing volume (e.g. on `move`) is fine.
	for _, vol := range s.Volumes {
		_, _ = p.Runner.Run("podman", p.podman("volume", "create", "--label", "poddle.pod="+s.Name, vol.Name)...)
	}

	res, err := p.Runner.Run("podman", args...)
	if err != nil {
		return "", fmt.Errorf("podman run: %w: %s", err, res.Stderr)
	}
	id := strings.TrimSpace(res.Stdout)

	// Provision the running container (e.g. install the harness). On failure the
	// container is left running so it can be inspected before cleanup.
	for _, cmd := range s.Setup {
		ex := p.podman("exec", id, "sh", "-c", cmd)
		if res, err := p.Runner.Run("podman", ex...); err != nil {
			return "", fmt.Errorf("setup %q failed: %w: %s (inspect/remove: poddle down %s)", cmd, err, res.Stderr, s.Name)
		}
	}
	return id, nil
}

// BrokerConfig is everything needed to launch the shared egress-broker
// container: it's dual-homed onto the shared egress network (for outbound
// internet) and, per pod, onto that pod's internal lock network (so locked
// pods can reach it without any other route out).
type BrokerConfig struct {
	Name      string // "poddle-broker"
	Image     string // resolved ref (PODDLE_BROKER_IMAGE or ghcr default)
	EgressNet string // "poddle-egress"
	RunDir    string // host dir bind-mounted to /run/poddle (holds the control socket)
	StateDir  string // host dir bind-mounted to /state (holds audit.db)
	// CoverDir, when set (the nightly e2e-coverage job, from the host's GOCOVERDIR),
	// bind-mounts a covdata dir into the broker and points GOCOVERDIR at it, so a
	// coverage-instrumented broker image writes its coverage on graceful shutdown.
	// Empty in production — no mount, no env.
	CoverDir string
	// Privsep opts the broker into the Phase-2 two-process model (the daemon forks a
	// keeper subprocess that holds the vault; a data-plane parser bug in the front
	// then can't read it). Default false — the in-process broker. Set via
	// `up --broker-privsep`.
	Privsep bool
}

// EnsureEgressNetwork creates the shared network the broker uses to reach the
// internet, if it doesn't already exist. Idempotent: an already-existing
// network is fine.
func (p *Provider) EnsureEgressNetwork(name string) error {
	res, err := p.Runner.Run("podman", p.podman("network", "create", name)...)
	if err != nil {
		if strings.Contains(res.Stderr, "already exists") {
			return nil
		}
		return fmt.Errorf("podman network create: %w: %s", err, res.Stderr)
	}
	return nil
}

// EnsureBroker launches the shared broker container, detached, dual-homed
// onto the egress network with the control/state dirs bind-mounted in. It holds
// all secrets and mounts no podman socket — pod-lifecycle work runs on the host
// (`poddle daemon autoscaled`) instead. Idempotent: a no-op if the broker is
// already running.
func (p *Provider) EnsureBroker(cfg BrokerConfig) error {
	// One query distinguishes running / stopped / absent, so a crashed or
	// stopped broker is restarted rather than wedging every future `up` on a
	// name conflict. Singleton by name (this is also what guarantees the audit
	// log's single writer).
	ps, err := p.Runner.Run("podman", p.podman("ps", "-a",
		"--filter", "name="+cfg.Name,
		"--format", "{{.Names}} {{.State}}")...)
	if err != nil {
		return fmt.Errorf("podman ps: %w: %s", err, ps.Stderr)
	}
	switch state := strings.TrimSpace(ps.Stdout); {
	case state == "":
		// absent -> create it below
	case strings.Contains(state, "running") || strings.Contains(state, "Up"):
		// Running, but not necessarily usable. When the LAST pod goes down its
		// lock network is removed (RemoveVolumesForPod -> DisconnectBrokerFromPod),
		// and a broker left attached to no lock network comes back unable to reach
		// any upstream: every subsequent request is answered 502 "upstream error"
		// even though the policy allowed it. `podman restart` does NOT clear it —
		// restoring the container restores the same stale network state — only a
		// fresh container does. So on an idle host (zero pods) recreate rather than
		// reuse. Safe precisely because there are no pods: nothing is attached to
		// this broker, the vault rehydrates from the on-disk connection store, and
		// the audit chain lives on the /state bind mount.
		attached, err := p.BrokerHasPodNetwork(cfg.Name)
		if err != nil || attached {
			return nil // in use (or undeterminable) -> leave it alone
		}
		if rmErr := p.Remove(cfg.Name); rmErr != nil {
			return nil // could not recycle it; reuse rather than fail the `up`
		}
		// fall through to create a fresh one
	default:
		// present but stopped/exited/created -> restart it, don't recreate.
		if r, err := p.Runner.Run("podman", p.podman("start", cfg.Name)...); err != nil {
			// Not every non-running state is startable: a host reboot (or a lost
			// rootless pause process) can leave the container wedged in
			// "stopping"/"removing"/"unknown", where `podman start` fails with
			// "container state improper" — and, because the name is taken, so does
			// every future `up`. Force-remove the carcass and fall through to
			// create a fresh one; the broker is stateless across restarts (vault in
			// memory, rehydrated from the on-disk connection store; audit db on the
			// /state bind mount), so recreating it is safe and the singleton holds.
			if rmErr := p.Remove(cfg.Name); rmErr != nil {
				return fmt.Errorf("podman start %s: %w: %s (force-remove also failed: %v)",
					cfg.Name, err, r.Stderr, rmErr)
			}
			break // fall out of the switch -> create below
		}
		return nil
	}

	args := p.podman("run", "-d",
		"--name", cfg.Name,
		"--network", cfg.EgressNet,
		// The broker is deliberately fail-closed: on a keeper death (two-process
		// mode) poddled exits non-zero "so its supervisor restarts it"
		// (poddled/serve.go). Nothing was actually supervising it — a crash left
		// the host with no broker until the next `up`, and a reboot left it down
		// entirely. --restart=always is that supervisor: podman restarts it on
		// crash, and podman-restart.service brings it back at boot (rootless needs
		// `systemctl --user enable podman-restart.service` + lingering; see
		// deploy/cloud-vm/). Pods are deliberately NOT restarted — they are
		// disposable shells whose state lives on named volumes.
		"--restart=always",
		// Least privilege: the broker holds every secret and does no privileged
		// work, so drop all Linux capabilities, forbid privilege escalation, and
		// run a read-only rootfs. Its only writes are the control socket and audit
		// db on the /run/poddle + /state mounts (still writable); /tmp is a tmpfs.
		// This caps what a data-plane parser bug (the pod-facing attack surface)
		// could reach.
		"--cap-drop=all",
		"--security-opt=no-new-privileges",
		"--read-only",
		"--tmpfs=/tmp",
		"-e", "XDG_STATE_HOME=/state",
		// The broker is containerized, so a pod's loopback upstream (a local
		// Postgres/Redis, or a local HTTP service) means the HOST's loopback, not
		// this container's. Dial such upstreams at the host route.
		"-e", "PODDLE_LOOPBACK_HOST=host.containers.internal",
		// Persist the egress-interception CA on the bind-mounted state dir (the
		// mount root, /state), so the broker signs leaves with the SAME CA `up`
		// injects into pods — and it survives broker restarts. StateDir mounts to
		// /state, so /state/egress-ca is the host's <StateDir>/egress-ca.
		"-e", "PODDLE_EGRESS_CA_DIR=/state/egress-ca",
		"-v", cfg.RunDir+":/run/poddle",
		"-v", cfg.StateDir+":/state",
	)
	// Forward the opt-in fresh-audit gate config from the host env so an operator
	// enables "no fresh audit -> no egress" for a cloud-connected broker without
	// rebuilding the image. Unset -> the gate stays off (default).
	for _, k := range []string{"PODDLE_REQUIRE_FRESH_AUDIT", "PODDLE_MAX_AUDIT_STALENESS"} {
		if v := os.Getenv(k); v != "" {
			args = append(args, "-e", k+"="+v)
		}
	}
	if cfg.CoverDir != "" {
		// Capture the containerized broker's own coverage: mount the host covdata
		// dir and point GOCOVERDIR at it. A read-only rootfs is fine — this is a
		// writable bind mount, like /state. Only meaningful for a -cover broker
		// image; a normal binary ignores GOCOVERDIR.
		args = append(args, "-e", "GOCOVERDIR=/covdata", "-v", cfg.CoverDir+":/covdata")
	}
	if cfg.Privsep {
		// Two-process broker: the daemon forks a keeper subprocess (holding the vault)
		// over a socketpair. Re-exec + fork + socketpair all work under the
		// --read-only/--cap-drop=all/--no-new-privileges lockdown above (proven by the
		// privsep spike); no capability or writable rootfs is needed.
		args = append(args, "-e", "PODDLE_BROKER_PRIVSEP=1")
	}
	// Pin the pod-facing listeners to fixed ports. The daemon defaults to
	// :0 (ephemeral), which is fine for a bare-host broker but wrong for this
	// containerized singleton: `up` bakes the broker's host:port into each pod's
	// HTTP_PROXY / PG / Redis env at creation time, and container env is
	// immutable. With ephemeral ports every broker restart — a crash restart, a
	// reboot, podman-restart.service — moves the port and silently strands every
	// existing pod ("connection refused" at the old port) even though the broker
	// is healthy and its IP is unchanged. Fixed ports make the broker genuinely
	// restartable. Safe because these bind inside the broker's OWN network
	// namespace, reachable only from the egress + per-pod lock networks, so they
	// cannot collide with the host or with a pod.
	args = append(args, cfg.Image,
		"--gateway-bind", "0.0.0.0:"+brokerGatewayPort,
		"--forward-bind", "0.0.0.0:"+brokerForwardPort,
		"--l4-redis-bind", "0.0.0.0:"+brokerRedisPort,
		"--l4-postgres-bind", "0.0.0.0:"+brokerPostgresPort,
	)

	res, err := p.Runner.Run("podman", args...)
	if err != nil {
		if strings.Contains(res.Stderr, "already in use") {
			return nil // a concurrent first-`up` won the create race; fine
		}
		return fmt.Errorf("podman run: %w: %s", err, res.Stderr)
	}
	return nil
}

// BrokerHasPodNetwork reports whether the broker is still attached to at least
// one pod lock network, i.e. whether any pod is currently relying on it. It is
// how EnsureBroker tells "idle host, safe to recycle" from "in use, hands off".
// An inspect error is reported so the caller can fail safe and keep the broker.
func (p *Provider) BrokerHasPodNetwork(name string) (bool, error) {
	const tmpl = `{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}`
	res, err := p.Runner.Run("podman", p.podman("inspect", "-f", tmpl, name)...)
	if err != nil {
		return false, fmt.Errorf("podman inspect %s: %w: %s", name, err, res.Stderr)
	}
	return strings.Contains(res.Stdout, lockNetPrefix), nil
}

// EnsurePodLockNetwork creates the internal (no-internet) network a locked
// pod's egress is pinned to, and returns its name. Idempotent: an
// already-existing network is fine.
func (p *Provider) EnsurePodLockNetwork(pod string) (string, error) {
	name := lockNetPrefix + pod
	res, err := p.Runner.Run("podman", p.podman("network", "create", "--internal", name)...)
	if err != nil {
		if strings.Contains(res.Stderr, "already exists") {
			return name, nil
		}
		return "", fmt.Errorf("podman network create --internal: %w: %s", err, res.Stderr)
	}
	return name, nil
}

// ConnectBrokerToPod attaches the broker container to a pod's internal lock
// network, so the pod can reach the broker despite having no other route out.
func (p *Provider) ConnectBrokerToPod(brokerName, pod string) error {
	res, err := p.Runner.Run("podman", p.podman("network", "connect", lockNetPrefix+pod, brokerName)...)
	if err != nil {
		// Idempotent: the shared broker stays attached across a pod's lifetime,
		// so `move`/autoscale-grow (which re-run buildSpec without a `down`)
		// re-connect an already-connected broker. That is success, not failure.
		if strings.Contains(res.Stderr, "already connected") {
			return nil
		}
		return fmt.Errorf("podman network connect: %w: %s", err, res.Stderr)
	}
	return nil
}

// BrokerIPOnPod returns the broker's IP address on a pod's internal lock
// network, so the pod can be told where to reach it.
func (p *Provider) BrokerIPOnPod(brokerName, pod string) (string, error) {
	tmpl := fmt.Sprintf(`{{(index .NetworkSettings.Networks "poddle-lock-%s").IPAddress}}`, pod)
	res, err := p.Runner.Run("podman", p.podman("inspect", "-f", tmpl, brokerName)...)
	if err != nil {
		return "", fmt.Errorf("podman inspect: %w: %s", err, res.Stderr)
	}
	ip := strings.TrimSpace(res.Stdout)
	if ip == "" {
		return "", fmt.Errorf("podman inspect: broker %q has no address on poddle-lock-%s", brokerName, pod)
	}
	return ip, nil
}

// DisconnectBrokerFromPod detaches the broker from a pod's lock network on
// teardown. Best-effort: errors (e.g. already disconnected) are ignored.
func (p *Provider) DisconnectBrokerFromPod(brokerName, pod string) error {
	_, _ = p.Runner.Run("podman", p.podman("network", "disconnect", lockNetPrefix+pod, brokerName)...)
	return nil
}

// Attach opens an interactive shell inside the sandbox (bash if present, else sh).
func (p *Provider) Attach(id string) error {
	args := p.podman("exec", "-it", id, "sh", "-c", "exec bash 2>/dev/null || exec sh")
	return p.Runner.RunInteractive("podman", args...)
}

// Exec runs a one-shot command in the sandbox, streaming its output to the
// caller's stdio (non-interactive).
func (p *Provider) Exec(id, command string) error {
	args := p.podman("exec", id, "sh", "-c", command)
	return p.Runner.RunInteractive("podman", args...)
}

// ExecDetached runs command in the background in the sandbox (podman exec -d)
// and returns as soon as it has started.
func (p *Provider) ExecDetached(id, command string) error {
	args := p.podman("exec", "-d", id, "sh", "-c", command)
	res, err := p.Runner.Run("podman", args...)
	if err != nil {
		return fmt.Errorf("podman exec -d: %w: %s", err, res.Stderr)
	}
	return nil
}

// PodInfo reads back a pod's reconstructable config from its labels, so `move`
// (and the autoscaler) can recreate the shell preserving everything but the
// size — with no working directory or template.
func (p *Provider) PodInfo(id string) (sandbox.PodInfo, error) {
	const f = `{{index .Config.Labels "poddle.image"}}|{{index .Config.Labels "poddle.size"}}|` +
		`{{index .Config.Labels "poddle.harness"}}|{{index .Config.Labels "poddle.identity"}}|` +
		`{{index .Config.Labels "poddle.repo"}}|{{index .Config.Labels "poddle.mode"}}|` +
		`{{index .Config.Labels "poddle.autoscale"}}`
	res, err := p.Runner.Run("podman", p.podman("inspect", "-f", f, id)...)
	if err != nil {
		return sandbox.PodInfo{}, fmt.Errorf("podman inspect: %w: %s", err, res.Stderr)
	}
	parts := strings.Split(strings.TrimSpace(res.Stdout), "|")
	if len(parts) != 7 {
		return sandbox.PodInfo{}, fmt.Errorf("podman inspect: unexpected label output %q", res.Stdout)
	}
	return sandbox.PodInfo{
		Image: parts[0], Size: parts[1], Harness: parts[2], Identity: parts[3],
		Repo: parts[4], Mode: parts[5], Autoscale: parts[6] == "true",
	}, nil
}

// ExecTTY runs an interactive (TTY) command in the sandbox — for resuming an
// interactive agent after a move.
func (p *Provider) ExecTTY(id, command string) error {
	return p.Runner.RunInteractive("podman", p.podman("exec", "-it", id, "sh", "-c", command)...)
}

// Stats returns live CPU/memory for running poddle-managed pods.
func (p *Provider) Stats() ([]sandbox.Stat, error) {
	ps, err := p.Runner.Run("podman", p.podman("ps", "--filter", "label=poddle.managed=true", "--format", "{{.Names}}")...)
	if err != nil {
		return nil, fmt.Errorf("podman ps: %w: %s", err, ps.Stderr)
	}
	names := strings.Fields(ps.Stdout)
	if len(names) == 0 {
		return nil, nil
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}"}, names...)
	res, err := p.Runner.Run("podman", p.podman(args...)...)
	if err != nil {
		return nil, fmt.Errorf("podman stats: %w: %s", err, res.Stderr)
	}
	var stats []sandbox.Stat
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 4 {
			continue
		}
		stats = append(stats, sandbox.Stat{Name: f[0], CPU: f[1], Mem: f[2], MemPerc: f[3]})
	}
	return stats, nil
}

// Pods returns a fleet + performance snapshot for the dashboard: every managed
// pod (from List) joined with its live CPU/memory (from Stats — best-effort, so
// a host without cgroup delegation still shows the fleet, just without numbers).
func (p *Provider) Pods() ([]sandbox.PodView, error) {
	list, err := p.List()
	if err != nil {
		return nil, err
	}
	stats, _ := p.Stats()
	byName := make(map[string]sandbox.Stat, len(stats))
	for _, s := range stats {
		byName[s.Name] = s
	}
	out := make([]sandbox.PodView, 0, len(list))
	for _, sb := range list {
		st := byName[sb.Name]
		out = append(out, sandbox.PodView{
			Name: sb.Name, State: sb.State, Size: sb.Size, Mode: sb.Mode,
			Policy: sb.Policy, Autoscale: sb.Autoscale,
			CPU: st.CPU, MemPerc: st.MemPerc, Mem: st.Mem,
		})
	}
	return out, nil
}

// AutoscaleStats returns live memory pressure for autoscale-opted-in pods (those
// labelled poddle.autoscale=true), joining each pod's mode/size labels with its
// current memory percent from `podman stats`. Empty (not an error) when none
// opted in — and it then skips the stats call — so the daemon's loop stays quiet
// on hosts that don't use autoscaling.
func (p *Provider) AutoscaleStats() ([]sandbox.MemStat, error) {
	ps, err := p.Runner.Run("podman", p.podman("ps",
		"--filter", "label=poddle.managed=true",
		"--filter", "label=poddle.autoscale=true",
		"--format", `{{.Names}}|{{index .Labels "poddle.mode"}}|{{index .Labels "poddle.size"}}`)...)
	if err != nil {
		return nil, fmt.Errorf("podman ps: %w: %s", err, ps.Stderr)
	}
	type meta struct{ mode, size string }
	byName := map[string]meta{}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(ps.Stdout), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 3 {
			continue
		}
		byName[f[0]] = meta{mode: f[1], size: f[2]}
		names = append(names, f[0])
	}
	if len(names) == 0 {
		return nil, nil
	}
	args := append([]string{"stats", "--no-stream", "--format", "{{.Name}}|{{.MemPerc}}"}, names...)
	res, err := p.Runner.Run("podman", p.podman(args...)...)
	if err != nil {
		return nil, fmt.Errorf("podman stats: %w: %s", err, res.Stderr)
	}
	var out []sandbox.MemStat
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 2 {
			continue
		}
		m := byName[f[0]]
		out = append(out, sandbox.MemStat{Name: f[0], Mode: m.mode, Size: m.size, MemPercent: parsePercent(f[1])})
	}
	return out, nil
}

// parsePercent turns podman's "85.34%" into 85.34 (0 on a parse failure).
func parsePercent(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	return v
}

// Resize live-updates a running sandbox's CPU ceiling and/or memory cap
// (cgroup update, no restart). cpus is a ceiling — idle pods still float to ~0.
func (p *Provider) Resize(id string, cpus float64, memory string) error {
	u := []string{"update"}
	if cpus > 0 {
		u = append(u, "--cpus", fmt.Sprintf("%g", cpus))
	}
	if memory != "" {
		u = append(u, "--memory", memory)
	}
	u = append(u, id)
	res, err := p.Runner.Run("podman", p.podman(u...)...)
	if err != nil {
		return fmt.Errorf("podman update: %w: %s", err, res.Stderr)
	}
	return nil
}

// RemoveVolumesForPod removes the named volumes labeled for a pod (its session
// state). Best-effort: no volumes is not an error.
func (p *Provider) RemoveVolumesForPod(pod string) error {
	res, err := p.Runner.Run("podman", p.podman("volume", "ls", "-q", "--filter", "label=poddle.pod="+pod)...)
	if err != nil {
		return fmt.Errorf("podman volume ls: %w: %s", err, res.Stderr)
	}
	names := strings.Fields(res.Stdout)
	if len(names) > 0 {
		if r, err := p.Runner.Run("podman", p.podman(append([]string{"volume", "rm"}, names...)...)...); err != nil {
			return fmt.Errorf("podman volume rm: %w: %s", err, r.Stderr)
		}
	}
	// Best-effort: detach the shared broker first so the subsequent network rm
	// isn't blocked by a still-attached container, then remove the pod's
	// egress-lockdown network, if any.
	_ = p.DisconnectBrokerFromPod("poddle-broker", pod)
	_, _ = p.Runner.Run("podman", p.podman("network", "rm", lockNetPrefix+pod)...)
	return nil
}

// Remove force-stops and deletes a sandbox by id or name.
func (p *Provider) Remove(id string) error {
	args := p.podman("rm", "-f", id)
	res, err := p.Runner.Run("podman", args...)
	if err != nil {
		return fmt.Errorf("podman rm: %w: %s", err, res.Stderr)
	}
	return nil
}

// containerJSON mirrors the fields poddle needs from `podman ps --format json`.
type containerJSON struct {
	ID     string            `json:"Id"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

func parseList(stdout string) ([]sandbox.Sandbox, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}
	var raw []containerJSON
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, fmt.Errorf("parse podman output: %w", err)
	}
	out := make([]sandbox.Sandbox, 0, len(raw))
	for _, c := range raw {
		out = append(out, sandbox.Sandbox{
			ID:        shortID(c.ID),
			Name:      c.Labels["poddle.name"],
			Template:  c.Labels["poddle.template"],
			Runtime:   c.Labels["poddle.runtime"],
			Size:      c.Labels["poddle.size"],
			Repo:      c.Labels["poddle.repo"],
			State:     mapState(c.State),
			Mode:      c.Labels["poddle.mode"],
			Autoscale: c.Labels["poddle.autoscale"] == "true",
			Policy:    c.Labels["poddle.policy"],
		})
	}
	return out, nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// mapState normalizes Podman container states to poddle's vocabulary.
func mapState(s string) string {
	switch strings.ToLower(s) {
	case "running":
		return "running"
	case "paused":
		return "paused"
	case "created", "exited", "stopped", "dead":
		return "stopped"
	default:
		return strings.ToLower(s)
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
