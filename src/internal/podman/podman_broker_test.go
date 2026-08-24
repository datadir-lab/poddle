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

func TestEnsureBroker_SkipsWhenAlreadyRunning(t *testing.T) {
	f := &exec.Fake{Outputs: map[string]string{"podman": "poddle-broker running\n"}}
	p := New(f, "")
	err := p.EnsureBroker(BrokerConfig{
		Name: "poddle-broker", Image: "poddle-broker:dev", EgressNet: "poddle-egress",
	})
	if err != nil {
		t.Fatalf("EnsureBroker: %v", err)
	}
	if got := joinCalls(f); strings.Contains(got, "run -d") || strings.Contains(got, "start") {
		t.Errorf("must not launch or start a second broker when one is already running:\n%s", got)
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
