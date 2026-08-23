package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicy_Decide(t *testing.T) {
	p := &Policy{
		AllowUpstreams: []string{"api.anthropic.com", ".internal"},
		DenyUpstreams:  []string{"metadata.google.internal"},
		Methods:        map[string][]string{"git.internal": {"GET"}},
	}
	cases := []struct {
		host, method string
		allow        bool
	}{
		{"api.anthropic.com", "POST", true},        // allow-listed
		{"evil.com", "GET", false},                 // not allow-listed -> default-deny
		{"api.internal", "POST", true},             // .internal subdomain allowed
		{"git.internal", "GET", true},              // allow-listed + GET permitted
		{"git.internal", "POST", false},            // method not permitted
		{"metadata.google.internal", "GET", false}, // deny-list wins over the .internal allow
	}
	for _, c := range cases {
		got, reason := p.Decide(c.host, c.method)
		if got != c.allow {
			t.Errorf("Decide(%q,%q) = %v (%q), want %v", c.host, c.method, got, reason, c.allow)
		}
	}
}

func TestPolicy_GlobalMethodRule(t *testing.T) {
	// A "*" catch-all restricts methods on every host; a specific host rule wins.
	p := &Policy{
		AllowUpstreams: []string{".example.com", "api.github.com"},
		Methods:        map[string][]string{"*": {"GET", "HEAD"}, "api.github.com": {"GET", "POST"}},
	}
	cases := []struct {
		host, method string
		allow        bool
	}{
		{"read.example.com", "GET", true},     // catch-all allows GET
		{"read.example.com", "HEAD", true},    // catch-all allows HEAD
		{"read.example.com", "POST", false},   // catch-all denies POST
		{"api.github.com", "POST", true},      // specific rule overrides the catch-all
		{"read.example.com", "CONNECT", true}, // CONNECT bypasses method rules (encrypted)
	}
	for _, c := range cases {
		if got, reason := p.Decide(c.host, c.method); got != c.allow {
			t.Errorf("Decide(%q,%q) = %v (%q), want %v", c.host, c.method, got, reason, c.allow)
		}
	}
}

func TestPolicy_EmptyAndNilAllowAll(t *testing.T) {
	var nilP *Policy
	if allow, _ := nilP.Decide("anything.com", "DELETE"); !allow {
		t.Error("a nil policy must allow all (no policy = no restriction)")
	}
	if allow, _ := (&Policy{}).Decide("anything.com", "DELETE"); !allow {
		t.Error("an empty policy must allow all")
	}
}

func TestPolicy_ConnectSkipsMethodRules(t *testing.T) {
	// A GET-only host: a CONNECT tunnel to it is governed by destination rules,
	// not the (encrypted) method, so it is allowed.
	p := &Policy{AllowUpstreams: []string{"api.x"}, Methods: map[string][]string{"api.x": {"GET"}}}
	if allow, reason := p.Decide("api.x", "CONNECT"); !allow {
		t.Errorf("CONNECT to an allow-listed host must not be denied by method rules (%q)", reason)
	}
	if allow, _ := p.Decide("evil.x", "CONNECT"); allow {
		t.Error("CONNECT to a non-allow-listed host must still be denied")
	}
}

func TestPolicy_MethodCaseInsensitive(t *testing.T) {
	p := &Policy{Methods: map[string][]string{"h": {"get"}}}
	if allow, _ := p.Decide("h", "GET"); !allow {
		t.Error("method matching should be case-insensitive")
	}
}

func TestStore_LoadsPolicy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ro.toml"), []byte(
		"allow_upstreams = [\"api.anthropic.com\"]\n"+
			"methods = { \"git.x\" = [\"GET\"] }\n"+
			"egress = \"block\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewFileStore(dir).Get("ro")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "ro" || len(p.AllowUpstreams) != 1 || p.Egress != "block" {
		t.Fatalf("loaded policy = %+v", p)
	}
	if allow, _ := p.Decide("api.anthropic.com", "POST"); !allow {
		t.Error("should allow the allow-listed host")
	}
	if allow, _ := p.Decide("git.x", "POST"); allow {
		t.Error("git.x is GET-only per the policy")
	}
	if _, err := NewFileStore(dir).Get("missing"); err == nil {
		t.Error("Get of a missing policy should error")
	}
}

func TestFileStore_PutListDelete(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "policies"))
	if err := s.Put(&Policy{Name: "ro", Description: "read-only egress", AllowUpstreams: []string{"api.x"}, Egress: "block", Monitor: true, Intercept: true}); err != nil {
		t.Fatal(err)
	}
	// Round-trips (including description, the monitor flag, and the intercept flag).
	got, err := s.Get("ro")
	if err != nil || got.Egress != "block" || len(got.AllowUpstreams) != 1 || got.Description != "read-only egress" || !got.Monitor || !got.Intercept {
		t.Fatalf("round-trip = %+v, err=%v", got, err)
	}
	if names, _ := s.List(); len(names) != 1 || names[0] != "ro" {
		t.Fatalf("List = %v", names)
	}
	if err := s.Delete("ro"); err != nil {
		t.Fatal(err)
	}
	if names, _ := s.List(); len(names) != 0 {
		t.Errorf("List after delete = %v", names)
	}
}

func TestFileStore_Default(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "policies"))
	_ = s.Put(&Policy{Name: "guard", AllowUpstreams: []string{"api.x"}})

	// Unset by default.
	if d, err := s.Default(); err != nil || d != "" {
		t.Fatalf("Default (unset) = %q err=%v, want empty", d, err)
	}
	// Set + read back.
	if err := s.SetDefault("guard"); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Default(); d != "guard" {
		t.Fatalf("Default = %q, want guard", d)
	}
	// The marker is not mistaken for a policy.
	if names, _ := s.List(); len(names) != 1 || names[0] != "guard" {
		t.Errorf("List should ignore the default marker; got %v", names)
	}
	// Deleting the default policy clears the dangling pointer.
	if err := s.Delete("guard"); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Default(); d != "" {
		t.Errorf("deleting the default policy should clear the default; got %q", d)
	}
	// Clearing an already-unset default is a no-op.
	if err := s.SetDefault(""); err != nil {
		t.Errorf("clearing an unset default should be a no-op; got %v", err)
	}
}

func TestLayered_Default(t *testing.T) {
	proj := NewFileStore(t.TempDir())
	global := NewFileStore(t.TempDir())
	l := Layered{Sources: []Store{proj, global}, Writer: global}

	// SetDefault writes through to the global (writer) store.
	if err := l.SetDefault("prod"); err != nil {
		t.Fatal(err)
	}
	if d, _ := global.Default(); d != "prod" {
		t.Errorf("SetDefault should write to the global writer; global default = %q", d)
	}
	if d, _ := l.Default(); d != "prod" {
		t.Errorf("Layered.Default = %q, want prod", d)
	}
	// A project default shadows the global one (project source is read first).
	_ = proj.SetDefault("project-default")
	if d, _ := l.Default(); d != "project-default" {
		t.Errorf("project default should shadow global; got %q", d)
	}
}

func TestLayered_ProjectShadowsGlobal(t *testing.T) {
	proj := NewFileStore(t.TempDir())
	global := NewFileStore(t.TempDir())
	_ = global.Put(&Policy{Name: "prod", AllowUpstreams: []string{"global.only"}})
	_ = global.Put(&Policy{Name: "shared", AllowUpstreams: []string{"global.shared"}})
	_ = proj.Put(&Policy{Name: "shared", AllowUpstreams: []string{"project.shared"}}) // shadows global

	l := Layered{Sources: []Store{proj, global}, Writer: global}

	shared, err := l.Get("shared")
	if err != nil || shared.AllowUpstreams[0] != "project.shared" {
		t.Fatalf("project should shadow global; got %+v err=%v", shared, err)
	}
	if p, _ := l.Get("prod"); p == nil || p.AllowUpstreams[0] != "global.only" {
		t.Errorf("global-only policy should resolve; got %+v", p)
	}
	if names, _ := l.List(); len(names) != 2 { // shared + prod, deduped
		t.Errorf("List should dedupe by name: %v", names)
	}
	// Writes go to the writer (global).
	if err := l.Put(&Policy{Name: "new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := global.Get("new"); err != nil {
		t.Errorf("Put should write through to the global (writer) store: %v", err)
	}
}
