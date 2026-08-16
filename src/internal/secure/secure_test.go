package secure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/sandbox"
)

func TestCheckMounts_AllowsUnrelated(t *testing.T) {
	tmp := t.TempDir()
	mounts := []sandbox.Mount{{Host: filepath.Join(tmp, "proj"), Container: "/workspace"}}
	if err := CheckMounts(mounts, []string{filepath.Join(tmp, "secret")}); err != nil {
		t.Errorf("an unrelated mount should be allowed, got %v", err)
	}
}

func TestCheckMounts_RejectsExactBlocked(t *testing.T) {
	tmp := t.TempDir()
	secret := filepath.Join(tmp, "secret")
	mounts := []sandbox.Mount{{Host: secret, Container: "/s"}}
	if err := CheckMounts(mounts, []string{secret}); err == nil {
		t.Error("mounting a blocked path itself should fail")
	}
}

func TestCheckMounts_RejectsParentExposingBlocked(t *testing.T) {
	tmp := t.TempDir()
	// Mounting the parent (tmp) would expose the blocked child (tmp/secret).
	mounts := []sandbox.Mount{{Host: tmp, Container: "/host"}}
	if err := CheckMounts(mounts, []string{filepath.Join(tmp, "secret")}); err == nil {
		t.Error("mounting a parent of a blocked path should fail")
	}
}

func TestCheckMounts_RejectsChildOfBlocked(t *testing.T) {
	tmp := t.TempDir()
	secret := filepath.Join(tmp, "secret")
	mounts := []sandbox.Mount{{Host: filepath.Join(secret, "sub"), Container: "/s"}}
	if err := CheckMounts(mounts, []string{secret}); err == nil {
		t.Error("mounting under a blocked path should fail")
	}
}

func TestDefaultBlocked_IncludesPoddleConfig(t *testing.T) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user config dir on this platform")
	}
	want := filepath.Join(cfg, "poddle")
	for _, b := range defaultBlocked() {
		if b == want {
			return
		}
	}
	t.Errorf("default block-list must include poddle's own config dir %q; got %v", want, defaultBlocked())
}
