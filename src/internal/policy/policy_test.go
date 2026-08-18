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

func TestPolicy_EmptyAndNilAllowAll(t *testing.T) {
	var nilP *Policy
	if allow, _ := nilP.Decide("anything.com", "DELETE"); !allow {
		t.Error("a nil policy must allow all (no policy = no restriction)")
	}
	if allow, _ := (&Policy{}).Decide("anything.com", "DELETE"); !allow {
		t.Error("an empty policy must allow all")
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
	p, err := NewStore(dir).Get("ro")
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
	if _, err := NewStore(dir).Get("missing"); err == nil {
		t.Error("Get of a missing policy should error")
	}
}
