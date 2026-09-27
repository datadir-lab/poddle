package podman

import (
	"errors"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/exec"
)

// joinCalls concatenates every recorded call (space-joined argv, one per line)
// so tests can assert with strings.Contains against the whole transcript.
func joinCalls(f *exec.Fake) string {
	var joined string
	for _, c := range f.Calls {
		joined += strings.Join(c, " ") + "\n"
	}
	return joined
}

func TestEnsureEgressNetwork_CreatesArgv(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.EnsureEgressNetwork("poddle-egress"); err != nil {
		t.Fatalf("EnsureEgressNetwork: %v", err)
	}
	if got := joinCalls(f); !strings.Contains(got, "network create poddle-egress") {
		t.Errorf("argv:\n%s", got)
	}
}

func TestEnsureEgressNetwork_FailClosed(t *testing.T) {
	f := &exec.Fake{Err: errors.New("podman: boom")}
	p := New(f, "")
	if err := p.EnsureEgressNetwork("poddle-egress"); err == nil {
		t.Fatal("EnsureEgressNetwork must fail closed when the runner errors")
	}
}

func TestEnsureBroker_RunsDetachedDualHomeMounts(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}} // health/ps: not running -> then run
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	joined := joinCalls(f)
	for _, want := range []string{
		"run -d", "--name poddle-broker", "--network poddle-egress",
		"-v /run/x:/run/poddle", "-v /state/x:/state",
		"poddle-broker:dev",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestEnsureBroker_HardensContainer(t *testing.T) {
	// The broker is the single process that holds every plaintext secret and
	// parses untrusted, pod-controlled bytes on the data plane. Run it least-
	// privileged: drop all capabilities, forbid privilege escalation, and use a
	// read-only rootfs (writes go to the /run/poddle + /state mounts) with a
	// tmpfs /tmp. A regression that quietly re-grants privilege is a real
	// security downgrade, so pin the flags.
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	joined := joinCalls(f)
	for _, want := range []string{
		"--cap-drop=all",
		"--security-opt=no-new-privileges",
		"--read-only",
		"--tmpfs=/tmp",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing hardening flag %q in:\n%s", want, joined)
		}
	}
}

func TestEnsureBroker_SetsLoopbackHostEnv(t *testing.T) {
	// The broker is containerized, so a pod's loopback upstream (a local
	// Postgres/Redis or HTTP service) means the HOST's loopback. The daemon reads
	// PODDLE_LOOPBACK_HOST to dial such upstreams at the host route.
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	if got := joinCalls(f); !strings.Contains(got, "PODDLE_LOOPBACK_HOST=host.containers.internal") {
		t.Errorf("missing PODDLE_LOOPBACK_HOST env in:\n%s", got)
	}
}

func TestEnsureBroker_SetsEgressCADirEnv(t *testing.T) {
	// The broker persists the egress-interception CA on its bind-mounted state dir
	// (the mount root, /state) so it signs leaves with the SAME CA `up` injects
	// into pods — and it survives restarts.
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	if got := joinCalls(f); !strings.Contains(got, "PODDLE_EGRESS_CA_DIR=/state/egress-ca") {
		t.Errorf("missing PODDLE_EGRESS_CA_DIR env in:\n%s", got)
	}
}

func TestEnsureBroker_MountsCoverDirWhenSet(t *testing.T) {
	// The nightly e2e-coverage job sets CoverDir (from GOCOVERDIR); the broker then
	// gets a covdata bind mount + GOCOVERDIR env so an instrumented image writes its
	// coverage to the host dir.
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:cover", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x", CoverDir: "/cov/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	got := joinCalls(f)
	if !strings.Contains(got, "GOCOVERDIR=/covdata") || !strings.Contains(got, "-v /cov/x:/covdata") {
		t.Errorf("coverage mount/env missing when CoverDir set:\n%s", got)
	}
}

func TestEnsureBroker_NoCoverMountWhenUnset(t *testing.T) {
	// Production: no CoverDir, so no coverage mount or env leaks into the run.
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	if got := joinCalls(f); strings.Contains(got, "GOCOVERDIR") || strings.Contains(got, "/covdata") {
		t.Errorf("no coverage mount expected when CoverDir empty:\n%s", got)
	}
}

func TestEnsureBroker_PrivsepEnvWhenSet(t *testing.T) {
	// Privsep=true opts the containerized broker into the two-process keeper model.
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x", Privsep: true,
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	if got := joinCalls(f); !strings.Contains(got, "-e PODDLE_BROKER_PRIVSEP=1") {
		t.Errorf("missing PODDLE_BROKER_PRIVSEP env when Privsep set:\n%s", got)
	}
}

func TestEnsureBroker_NoPrivsepEnvByDefault(t *testing.T) {
	// Default (in-process) broker: the two-process env must not leak in.
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	if got := joinCalls(f); strings.Contains(got, "PODDLE_BROKER_PRIVSEP") {
		t.Errorf("PODDLE_BROKER_PRIVSEP must not appear by default:\n%s", got)
	}
}

// A running broker that is still serving a pod is reused as-is: no second
// broker launched, and not recycled either — recycling would cut the live pod's
// only route out. (The idle, no-pods case is covered by
// TestEnsureBroker_RecyclesIdleBrokerWithNoPodNetworks.)
func TestEnsureBroker_SkipsWhenAlreadyRunning(t *testing.T) {
	got := ensureRunningBroker(t, &runningBrokerRunner{nets: "poddle-egress poddle-lock-app"})
	for _, bad := range []string{"run -d", "start", "rm -f"} {
		if strings.Contains(got, bad) {
			t.Errorf("must not %q a broker that is already running and in use:\n%s", bad, got)
		}
	}
}

func TestEnsureBroker_StartsStoppedBroker(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "poddle-broker exited\n"}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	got := joinCalls(f)
	if !strings.Contains(got, "start poddle-broker") {
		t.Errorf("a stopped broker must be restarted, not left wedged:\n%s", got)
	}
	if strings.Contains(got, "run -d") {
		t.Errorf("a stopped broker must be started, not recreated (name conflict):\n%s", got)
	}
}

func TestEnsureBroker_NeverMountsPodmanSock(t *testing.T) {
	// Security invariant: the broker holds all secrets and does no pod-lifecycle
	// work, so it must never be handed a podman socket. The autoscaler runs on
	// the host instead.
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	got := joinCalls(f)
	if strings.Contains(got, "podman.sock") {
		t.Errorf("broker must never mount a podman socket:\n%s", got)
	}
	if strings.Contains(got, "PODDLE_PODMAN_URL") {
		t.Errorf("broker must not receive a podman URL env:\n%s", got)
	}
}

func TestEnsureBroker_FailClosed(t *testing.T) {
	f := &exec.Fake{Err: errors.New("podman: cannot run")}
	p := New(f, "")
	if err := p.EnsureBroker(BrokerConfig{Name: "poddle-broker", Image: "x", EgressNet: "poddle-egress"}); err == nil {
		t.Fatal("EnsureBroker must fail closed when the runner errors")
	}
}

func TestEnsurePodLockNetwork_Internal(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	name, err := p.EnsurePodLockNetwork("box")
	if err != nil || name != "poddle-lock-box" {
		t.Fatalf("got %q, %v", name, err)
	}
	if got := joinCalls(f); !strings.Contains(got, "network create --internal poddle-lock-box") {
		t.Errorf("argv:\n%s", got)
	}
}

func TestEnsurePodLockNetwork_FailClosed(t *testing.T) {
	f := &exec.Fake{Err: errors.New("netavark: internal networks unsupported")}
	p := New(f, "")
	if _, err := p.EnsurePodLockNetwork("box"); err == nil {
		t.Fatal("EnsurePodLockNetwork must fail closed when the runner errors")
	}
}

func TestConnectBrokerToPod_Argv(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.ConnectBrokerToPod("poddle-broker", "box"); err != nil {
		t.Fatal(err)
	}
	if got := joinCalls(f); !strings.Contains(got, "network connect poddle-lock-box poddle-broker") {
		t.Errorf("argv:\n%s", got)
	}
}

func TestConnectBrokerToPod_FailClosed(t *testing.T) {
	f := &exec.Fake{Err: errors.New("boom")}
	p := New(f, "")
	if err := p.ConnectBrokerToPod("poddle-broker", "box"); err == nil {
		t.Fatal("ConnectBrokerToPod must fail closed when the runner errors")
	}
}

func TestConnectBrokerToPod_ToleratesAlreadyConnected(t *testing.T) {
	// move/autoscale-grow re-run buildSpec without a `down`, so the broker is
	// still attached — podman errors "already connected", which is success.
	f := &exec.Fake{
		Err:    errors.New("exit status 125"),
		Stderr: "Error: container poddle-broker is already connected to network poddle-lock-box",
	}
	p := New(f, "")
	if err := p.ConnectBrokerToPod("poddle-broker", "box"); err != nil {
		t.Fatalf("ConnectBrokerToPod must tolerate an already-connected broker: %v", err)
	}
}

func TestBrokerIPOnPod_ParsesInspect(t *testing.T) {
	// podman inspect -f '{{(index .NetworkSettings.Networks "poddle-lock-box").IPAddress}}' poddle-broker
	f := &exec.Fake{Outputs: map[string]string{"podman": "10.89.3.4\n"}}
	p := New(f, "")
	ip, err := p.BrokerIPOnPod("poddle-broker", "box")
	if err != nil || ip != "10.89.3.4" {
		t.Fatalf("ip=%q err=%v", ip, err)
	}
	if got := joinCalls(f); !strings.Contains(got, `poddle-lock-box`) || !strings.Contains(got, "poddle-broker") {
		t.Errorf("argv:\n%s", got)
	}
}

func TestBrokerIPOnPod_FailClosed(t *testing.T) {
	f := &exec.Fake{Err: errors.New("boom")}
	p := New(f, "")
	if _, err := p.BrokerIPOnPod("poddle-broker", "box"); err == nil {
		t.Fatal("BrokerIPOnPod must fail closed when the runner errors")
	}
}

func TestBrokerIPOnPod_EmptyIsError(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "\n"}}
	p := New(f, "")
	if _, err := p.BrokerIPOnPod("poddle-broker", "box"); err == nil {
		t.Fatal("BrokerIPOnPod must error when the parsed IP is empty")
	}
}

func TestDisconnectBrokerFromPod_Argv(t *testing.T) {
	f := &exec.Fake{}
	p := New(f, "")
	if err := p.DisconnectBrokerFromPod("poddle-broker", "box"); err != nil {
		t.Fatalf("DisconnectBrokerFromPod: %v", err)
	}
	if got := joinCalls(f); !strings.Contains(got, "network disconnect poddle-lock-box poddle-broker") {
		t.Errorf("argv:\n%s", got)
	}
}

func TestDisconnectBrokerFromPod_BestEffortIgnoresError(t *testing.T) {
	f := &exec.Fake{Err: errors.New("already disconnected")}
	p := New(f, "")
	if err := p.DisconnectBrokerFromPod("poddle-broker", "box"); err != nil {
		t.Fatalf("DisconnectBrokerFromPod must be best-effort (never error): %v", err)
	}
}

// wedgedBrokerRunner plays a host where the broker container is stuck in a
// non-startable state (the "stopping" carcass a reboot or a lost rootless pause
// process leaves behind): `ps` reports it present, `start` fails the way podman
// really does, and `rm -f` / `run` succeed.
type wedgedBrokerRunner struct {
	calls     [][]string
	state     string // what `ps -a` reports
	rmFails   bool
	startErrs int
}

func (r *wedgedBrokerRunner) Run(name string, args ...string) (exec.Result, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	switch {
	case hasArg(args, "ps"):
		return exec.Result{Stdout: "poddle-broker " + r.state + "\n"}, nil
	case hasArg(args, "start"):
		r.startErrs++
		return exec.Result{Stderr: `Error: unable to start container "poddle-broker": ` +
			"container state improper"}, errors.New("exit status 125")
	case hasArg(args, "rm"):
		if r.rmFails {
			return exec.Result{Stderr: "rm refused"}, errors.New("exit status 2")
		}
		return exec.Result{Stdout: "poddle-broker\n"}, nil
	}
	return exec.Result{Stdout: "broker-id\n"}, nil
}

func (r *wedgedBrokerRunner) RunInteractive(name string, args ...string) error { return nil }

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func (r *wedgedBrokerRunner) transcript() string {
	var joined string
	for _, c := range r.calls {
		joined += strings.Join(c, " ") + "\n"
	}
	return joined
}

// A broker wedged in a non-startable state must not wedge every future `up`:
// EnsureBroker force-removes the carcass and recreates it.
func TestEnsureBroker_RecreatesWhenStartFailsOnWedgedState(t *testing.T) {
	for _, state := range []string{"stopping", "removing", "unknown", "paused"} {
		t.Run(state, func(t *testing.T) {
			r := &wedgedBrokerRunner{state: state}
			p := New(r, "")
			if err := p.EnsureBroker(BrokerConfig{
				Name: "poddle-broker", Image: "img", EgressNet: "poddle-egress",
				RunDir: "/run/poddle", StateDir: "/state",
			}); err != nil {
				t.Fatalf("EnsureBroker must self-heal a %q broker, got: %v", state, err)
			}
			got := r.transcript()
			if r.startErrs != 1 {
				t.Errorf("expected one failed start attempt, got %d", r.startErrs)
			}
			if !strings.Contains(got, "rm -f poddle-broker") {
				t.Errorf("wedged broker must be force-removed:\n%s", got)
			}
			if !strings.Contains(got, "run -d --name poddle-broker") {
				t.Errorf("wedged broker must be recreated:\n%s", got)
			}
		})
	}
}

// If the carcass cannot even be removed, EnsureBroker must fail closed and say
// why — never silently proceed to a `run` that will collide on the name.
func TestEnsureBroker_FailsClosedWhenForceRemoveFails(t *testing.T) {
	r := &wedgedBrokerRunner{state: "stopping", rmFails: true}
	p := New(r, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "img", EgressNet: "poddle-egress",
		RunDir: "/run/poddle", StateDir: "/state",
	})
	if err == nil {
		t.Fatal("EnsureBroker must fail closed when the wedged broker cannot be removed")
	}
	if !strings.Contains(err.Error(), "force-remove also failed") {
		t.Errorf("error should name the failed cleanup, got: %v", err)
	}
	if strings.Contains(r.transcript(), "run -d") {
		t.Errorf("must not attempt create after a failed force-remove:\n%s", r.transcript())
	}
}

// runningBrokerRunner plays a host where the broker is already running. nets is
// what `podman inspect` reports for its network attachments.
type runningBrokerRunner struct {
	calls      [][]string
	nets       string
	inspectErr bool
}

func (r *runningBrokerRunner) Run(name string, args ...string) (exec.Result, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	switch {
	case hasArg(args, "ps"):
		return exec.Result{Stdout: "poddle-broker running\n"}, nil
	case hasArg(args, "inspect"):
		if r.inspectErr {
			return exec.Result{Stderr: "no such object"}, errors.New("exit status 125")
		}
		return exec.Result{Stdout: r.nets + "\n"}, nil
	}
	return exec.Result{Stdout: "broker-id\n"}, nil
}

func (r *runningBrokerRunner) RunInteractive(name string, args ...string) error { return nil }

func (r *runningBrokerRunner) transcript() string {
	var joined string
	for _, c := range r.calls {
		joined += strings.Join(c, " ") + "\n"
	}
	return joined
}

func ensureRunningBroker(t *testing.T, r *runningBrokerRunner) string {
	t.Helper()
	if err := New(r, "").EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "img", EgressNet: "poddle-egress",
		RunDir: "/run/poddle", StateDir: "/state",
	}); err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	return r.transcript()
}

// On an idle host (no pod lock networks) the running broker is stale: it answers
// 502 on every upstream. Recycle it instead of handing back a broken broker.
func TestEnsureBroker_RecyclesIdleBrokerWithNoPodNetworks(t *testing.T) {
	got := ensureRunningBroker(t, &runningBrokerRunner{nets: "poddle-egress"})
	if !strings.Contains(got, "rm -f poddle-broker") {
		t.Errorf("idle broker must be force-removed:\n%s", got)
	}
	if !strings.Contains(got, "run -d --name poddle-broker") {
		t.Errorf("idle broker must be recreated:\n%s", got)
	}
}

// If the attachment cannot be determined, keep the existing broker: guessing
// "idle" and recycling could cut a live pod's only route out.
func TestEnsureBroker_KeepsBrokerWhenInspectFails(t *testing.T) {
	got := ensureRunningBroker(t, &runningBrokerRunner{inspectErr: true})
	if strings.Contains(got, "rm -f") || strings.Contains(got, "run -d") {
		t.Errorf("must fail safe and keep the broker when inspect fails:\n%s", got)
	}
}

// The broker is fail-closed by design: poddled exits non-zero on a keeper death
// expecting a supervisor to restart it. --restart=always IS that supervisor, and
// it is also what brings the broker back after a host reboot. Losing this flag
// silently reintroduces "no broker until the next `up`".
func TestEnsureBroker_SetsRestartPolicy(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	if got := joinCalls(f); !strings.Contains(got, "--restart=always") {
		t.Errorf("broker must be launched under a restart policy:\n%s", got)
	}
}

// `up` bakes the broker's address:port into each pod's proxy/datastore env at
// creation time, and container env is immutable. Ephemeral (:0) listeners would
// therefore strand every existing pod on each broker restart, so the
// containerized broker must bind FIXED pod-facing ports.
func TestEnsureBroker_PinsPodFacingPorts(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": ""}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
		RunDir: "/run/x", StateDir: "/state/x",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	got := joinCalls(f)
	for _, want := range []string{
		"--gateway-bind 0.0.0.0:" + brokerGatewayPort,
		"--forward-bind 0.0.0.0:" + brokerForwardPort,
		"--l4-redis-bind 0.0.0.0:" + brokerRedisPort,
		"--l4-postgres-bind 0.0.0.0:" + brokerPostgresPort,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing pinned listener %q in:\n%s", want, got)
		}
	}
	// The flags are CMD args, so they must come AFTER the image ref or podman
	// treats them as `podman run` flags and the launch fails.
	if strings.Index(got, "poddle-broker:dev") > strings.Index(got, "--gateway-bind") {
		t.Errorf("listener flags must follow the image ref:\n%s", got)
	}
	// All four must differ, or the daemon fails to bind the second one.
	seen := map[string]bool{}
	for _, port := range []string{brokerGatewayPort, brokerForwardPort, brokerRedisPort, brokerPostgresPort} {
		if seen[port] {
			t.Errorf("duplicate broker listener port %s", port)
		}
		seen[port] = true
	}
}
