package poddled

import (
	"path/filepath"
	"testing"
)

func TestSocketPath_HonorsPoddleSocket(t *testing.T) {
	want := filepath.FromSlash("/tmp/custom/poddled.sock")
	t.Setenv("PODDLE_SOCKET", want)
	t.Setenv("XDG_RUNTIME_DIR", filepath.FromSlash("/should/be/ignored"))
	if got := SocketPath(); got != want {
		t.Errorf("SocketPath = %q, want the PODDLE_SOCKET override %q", got, want)
	}
}
