package secure

import (
	"os"
	"path/filepath"
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
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

func TestScanMounts_FindsCredentialFiles(t *testing.T) {
	tmp := t.TempDir()
	for _, f := range []string{".env", "id_rsa", ".env.example", "README.md"} {
		if err := os.WriteFile(filepath.Join(tmp, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(tmp, "conf")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "server.pem"), []byte("x"), 0o600)

	got := map[string]bool{}
	for _, f := range ScanMounts([]sandbox.Mount{{Host: tmp, Container: "/w"}}) {
		got[filepath.Base(f.Path)] = true
	}
	for _, want := range []string{".env", "id_rsa", "server.pem"} {
		if !got[want] {
			t.Errorf("expected %q to be flagged; got %v", want, got)
		}
	}
	if got[".env.example"] || got["README.md"] {
		t.Errorf("example/non-secret files should not be flagged; got %v", got)
	}
}

func TestScanMounts_FileMount(t *testing.T) {
	tmp := t.TempDir()
	key := filepath.Join(tmp, "id_ed25519")
	if err := os.WriteFile(key, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if f := ScanMounts([]sandbox.Mount{{Host: key, Container: "/k"}}); len(f) != 1 {
		t.Errorf("a credential file mounted directly should be flagged, got %v", f)
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
