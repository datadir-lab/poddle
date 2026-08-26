package broker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStateDirPersister_PersistAndReadBack(t *testing.T) {
	dir := t.TempDir()
	p := NewStateDirPersister(dir)

	rotated := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	m := connMirror{RefreshToken: "r2", RotatedAt: rotated}

	if err := p.Persist("gh", m); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	path := filepath.Join(dir, "gh.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mirror file perm = %v, want 0600", info.Mode().Perm())
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !jsonHasKV(t, b, "refresh", "r2") {
		t.Errorf("mirror JSON missing %q:%q, got %s", "refresh", "r2", b)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	if _, ok := raw["rotated_at"]; !ok {
		t.Errorf("mirror JSON missing %q key, got %s", "rotated_at", b)
	}

	var got connMirror
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal to connMirror: %v", err)
	}
	if got != m {
		t.Errorf("round-tripped connMirror = %+v, want %+v", got, m)
	}
}

func TestStateDirPersister_Overwrite(t *testing.T) {
	dir := t.TempDir()
	p := NewStateDirPersister(dir)

	first := connMirror{RefreshToken: "r1", RotatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	second := connMirror{RefreshToken: "r2-rotated", RotatedAt: time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)}

	if err := p.Persist("gh", first); err != nil {
		t.Fatalf("Persist(first): %v", err)
	}
	if err := p.Persist("gh", second); err != nil {
		t.Fatalf("Persist(second): %v", err)
	}

	path := filepath.Join(dir, "gh.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var got connMirror
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != second {
		t.Errorf("after overwrite, connMirror = %+v, want %+v", got, second)
	}
}

func TestStateDirPersister_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	p := NewStateDirPersister(dir)

	for _, name := range []string{"../evil", "a/b", "a\\b", "..", "."} {
		if err := p.Persist(name, connMirror{RefreshToken: "x"}); err == nil {
			t.Errorf("Persist(%q) should have been rejected, got nil error", name)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("rejected Persist calls should write nothing, found %d entries: %v", len(entries), entries)
	}
}

// TestConnMirror_JSONTagsMatchOAuthMaterial pins the byte-for-byte tag
// invariant with connector.OAuthMaterial (broker cannot import connector to
// check this directly — see the comment on connMirror).
func TestConnMirror_JSONTagsMatchOAuthMaterial(t *testing.T) {
	m := connMirror{
		AccessToken:   "a",
		RefreshToken:  "r",
		TokenEndpoint: "t",
		ClientID:      "c",
		ClientSecret:  "s",
		Scope:         "sc",
		ExpiresAt:     time.Now(),
		RotatedAt:     time.Now(),
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"access", "refresh", "token_endpoint", "client_id", "client_secret", "scope", "expires_at", "rotated_at"}
	if len(raw) != len(want) {
		t.Fatalf("connMirror JSON has %d keys, want %d: %v", len(raw), len(want), raw)
	}
	for _, k := range want {
		if _, ok := raw[k]; !ok {
			t.Errorf("connMirror JSON missing key %q (drifted from connector.OAuthMaterial?)", k)
		}
	}
}

// jsonHasKV is a tiny helper asserting the raw JSON bytes contain a string
// field with the given key/value, without caring about surrounding key
// ordering.
func jsonHasKV(t *testing.T, b []byte, key, value string) bool {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, ok := raw[key]
	if !ok {
		return false
	}
	s, ok := got.(string)
	return ok && s == value
}
