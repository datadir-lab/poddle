package poddled

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditDBPath_HonorsXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.FromSlash("/custom/state"))
	got := AuditDBPath()
	if !strings.Contains(got, filepath.FromSlash("custom/state")) {
		t.Errorf("AuditDBPath = %q, want it under XDG_STATE_HOME", got)
	}
	if !strings.HasSuffix(got, filepath.FromSlash("poddle/audit.db")) {
		t.Errorf("AuditDBPath = %q, want .../poddle/audit.db", got)
	}
}

func TestAuditDBPath_FallsBackWhenXDGUnset(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	got := AuditDBPath()
	if !strings.HasSuffix(got, filepath.FromSlash("poddle/audit.db")) {
		t.Errorf("AuditDBPath = %q, want a .../poddle/audit.db fallback", got)
	}
}

func TestEgressCADir_SharesTheStateMountRootWithAudit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.FromSlash("/custom/state"))
	// EgressCADir must live directly beside the audit db (same parent dir), because
	// that parent is the host dir EnsureRunning bind-mounts to /state. That is what
	// lets the containerized broker (PODDLE_EGRESS_CA_DIR=/state/egress-ca) and `up`
	// (EgressCADir on the host) resolve one shared CA file.
	ca := EgressCADir()
	if want := filepath.Dir(AuditDBPath()); filepath.Dir(ca) != want {
		t.Errorf("EgressCADir parent = %q, want %q (the state-mount source shared with audit)", filepath.Dir(ca), want)
	}
	if filepath.Base(ca) != "egress-ca" {
		t.Errorf("EgressCADir = %q, want a .../egress-ca dir", ca)
	}
}

func TestSocketPath_FallsBackWhenXDGUnset(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := SocketPath()
	if !strings.HasSuffix(got, filepath.FromSlash("poddle/poddled.sock")) {
		t.Errorf("SocketPath = %q, want a .../poddle/poddled.sock fallback", got)
	}
}
