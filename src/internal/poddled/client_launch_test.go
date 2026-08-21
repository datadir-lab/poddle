package poddled

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datadir-lab/poddle/src/internal/podman"
)

// fakeLauncher is a brokerLauncher test double that records calls so
// EnsureRunning's ordering and fail-closed behavior can be asserted without a
// real podman engine.
type fakeLauncher struct {
	netErr    error
	brokerErr error

	netCalls    []string
	brokerCfg   podman.BrokerConfig
	brokerCalls int
	// netCalledBeforeBroker is latched the first time EnsureBroker runs, so the
	// test can assert ordering even though both slices/counts only grow.
	netCalledBeforeBroker bool
}

func (f *fakeLauncher) EnsureEgressNetwork(name string) error {
	f.netCalls = append(f.netCalls, name)
	return f.netErr
}

func (f *fakeLauncher) EnsureBroker(cfg podman.BrokerConfig) error {
	if f.brokerCalls == 0 {
		f.netCalledBeforeBroker = len(f.netCalls) > 0
	}
	f.brokerCalls++
	f.brokerCfg = cfg
	return f.brokerErr
}

// unhealthyClient returns a Client pointed at a socket nothing is listening
// on, so Health() always fails and EnsureRunning falls through to the
// launcher path.
func unhealthyClient(t *testing.T) *Client {
	t.Helper()
	return NewClient(filepath.Join(t.TempDir(), "poddled.sock"))
}

// EnsureRunning must fail closed when the broker fails to start: it returns
// the launcher's error and must NOT fall back to spawning a host process (the
// old exec.Command(self, "daemon", ...) path). If it had fallen back to
// spawning and waiting, this would take ~5s (the health-wait deadline) and
// return a "did not become healthy" error instead of the launcher's error.
func TestEnsureRunning_FailClosed_OnBrokerError(t *testing.T) {
	c := unhealthyClient(t)
	wantErr := errors.New("boom: broker failed to start")
	fl := &fakeLauncher{brokerErr: wantErr}
	c.launcher = fl

	start := time.Now()
	err := c.EnsureRunning()
	elapsed := time.Since(start)

	if !errors.Is(err, wantErr) {
		t.Fatalf("EnsureRunning() error = %v, want %v", err, wantErr)
	}
	if elapsed > time.Second {
		t.Errorf("EnsureRunning took %s; expected an immediate fail-closed return, not the 5s health-wait fallback", elapsed)
	}
	if fl.brokerCalls != 1 {
		t.Errorf("EnsureBroker called %d times, want 1", fl.brokerCalls)
	}
}

// EnsureRunning must also fail closed when the egress network can't be
// ensured, and must never call EnsureBroker in that case.
func TestEnsureRunning_FailClosed_OnEgressNetworkError(t *testing.T) {
	c := unhealthyClient(t)
	wantErr := errors.New("boom: network create failed")
	fl := &fakeLauncher{netErr: wantErr}
	c.launcher = fl

	err := c.EnsureRunning()

	if !errors.Is(err, wantErr) {
		t.Fatalf("EnsureRunning() error = %v, want %v", err, wantErr)
	}
	if fl.brokerCalls != 0 {
		t.Errorf("EnsureBroker called %d times, want 0 (must not run after EnsureEgressNetwork fails)", fl.brokerCalls)
	}
}

// EnsureRunning must call EnsureEgressNetwork("poddle-egress") before
// EnsureBroker, and pass a BrokerConfig whose RunDir/StateDir are derived from
// the client's socket path and AuditDBPath().
func TestEnsureRunning_CallsEgressNetworkBeforeBroker_WithDerivedConfig(t *testing.T) {
	c := unhealthyClient(t)
	fl := &fakeLauncher{brokerErr: errors.New("stop after inspecting the call")}
	c.launcher = fl

	_ = c.EnsureRunning()

	if len(fl.netCalls) != 1 || fl.netCalls[0] != "poddle-egress" {
		t.Fatalf("EnsureEgressNetwork calls = %v, want exactly [\"poddle-egress\"]", fl.netCalls)
	}
	if fl.brokerCalls != 1 {
		t.Fatalf("EnsureBroker called %d times, want 1", fl.brokerCalls)
	}
	if !fl.netCalledBeforeBroker {
		t.Error("EnsureEgressNetwork must be called before EnsureBroker")
	}

	wantRunDir := filepath.Dir(c.socket)
	wantStateDir := filepath.Dir(AuditDBPath())
	if fl.brokerCfg.Name != "poddle-broker" {
		t.Errorf("BrokerConfig.Name = %q, want %q", fl.brokerCfg.Name, "poddle-broker")
	}
	if fl.brokerCfg.EgressNet != "poddle-egress" {
		t.Errorf("BrokerConfig.EgressNet = %q, want %q", fl.brokerCfg.EgressNet, "poddle-egress")
	}
	if fl.brokerCfg.RunDir != wantRunDir {
		t.Errorf("BrokerConfig.RunDir = %q, want %q", fl.brokerCfg.RunDir, wantRunDir)
	}
	if fl.brokerCfg.StateDir != wantStateDir {
		t.Errorf("BrokerConfig.StateDir = %q, want %q", fl.brokerCfg.StateDir, wantStateDir)
	}
	if fl.brokerCfg.Image == "" {
		t.Error("BrokerConfig.Image must be resolved, not empty")
	}
}

// EnsureRunning must create the broker's RunDir and StateDir (and, implicitly,
// their parents — e.g. a test/CI XDG_RUNTIME_DIR) before launching, because
// podman refuses to bind-mount a source path that does not exist. Regression
// test for the containerized-launch bug where these were never created.
func TestEnsureRunning_CreatesRunAndStateDirs(t *testing.T) {
	base := t.TempDir()
	sockDir := filepath.Join(base, "run", "poddle") // does not exist yet
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	c := NewClient(filepath.Join(sockDir, "poddled.sock"))
	c.launcher = &fakeLauncher{brokerErr: errors.New("stop after dirs are made")}

	_ = c.EnsureRunning()

	if _, err := os.Stat(sockDir); err != nil {
		t.Errorf("EnsureRunning must create the run dir %q: %v", sockDir, err)
	}
	wantStateDir := filepath.Dir(AuditDBPath())
	if _, err := os.Stat(wantStateDir); err != nil {
		t.Errorf("EnsureRunning must create the state dir %q: %v", wantStateDir, err)
	}
}

// resolveBrokerImage honors PODDLE_BROKER_IMAGE and falls back to the ghcr
// default otherwise.
func TestResolveBrokerImage(t *testing.T) {
	t.Setenv("PODDLE_BROKER_IMAGE", "")
	if got := resolveBrokerImage(); got != "ghcr.io/datadir-lab/poddle-broker:latest" {
		t.Errorf("resolveBrokerImage() = %q, want the ghcr default", got)
	}

	t.Setenv("PODDLE_BROKER_IMAGE", "example.com/custom-broker:v1")
	if got := resolveBrokerImage(); got != "example.com/custom-broker:v1" {
		t.Errorf("resolveBrokerImage() = %q, want the env override", got)
	}
}
