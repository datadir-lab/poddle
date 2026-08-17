// Package podman implements the sandbox provider backed by Podman, local or
// remote. A remote host is addressed via Podman's --url ssh://... transport, so
// the same code lists/creates/execs sandboxes regardless of where they run.
package podman

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
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
	args := p.podman("run", "-d",
		"--name", s.Name,
		"--label", "poddle.managed=true",
		"--label", "poddle.name="+s.Name,
		"--label", "poddle.template="+s.Template,
		"--label", "poddle.runtime="+s.Runtime,
		"--label", "poddle.size="+s.Size,
		"--label", "poddle.repo="+s.Repo,
		"--label", "poddle.mode="+s.Mode,
	)
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

// PodMode reads a pod's poddle.mode label (how its agent runs), for resume on move.
func (p *Provider) PodMode(id string) (string, error) {
	res, err := p.Runner.Run("podman", p.podman("inspect", "-f", `{{index .Config.Labels "poddle.mode"}}`, id)...)
	if err != nil {
		return "", fmt.Errorf("podman inspect: %w: %s", err, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout), nil
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
	if len(names) == 0 {
		return nil
	}
	if r, err := p.Runner.Run("podman", p.podman(append([]string{"volume", "rm"}, names...)...)...); err != nil {
		return fmt.Errorf("podman volume rm: %w: %s", err, r.Stderr)
	}
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
			ID:       shortID(c.ID),
			Name:     c.Labels["poddle.name"],
			Template: c.Labels["poddle.template"],
			Runtime:  c.Labels["poddle.runtime"],
			Size:     c.Labels["poddle.size"],
			Repo:     c.Labels["poddle.repo"],
			State:    mapState(c.State),
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
