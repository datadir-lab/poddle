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

func TestSocketPath_FallsBackWhenXDGUnset(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := SocketPath()
	if !strings.HasSuffix(got, filepath.FromSlash("poddle/poddled.sock")) {
		t.Errorf("SocketPath = %q, want a .../poddle/poddled.sock fallback", got)
	}
}
