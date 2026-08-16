package connector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/broker"
)

func TestLoadDefinition_BuiltinOverrideUnknown(t *testing.T) {
	if d, err := LoadDefinition("", "forgejo"); err != nil || d.Mode != "basic" {
		t.Errorf("builtin forgejo = %+v, %v", d, err)
	}
	dir := t.TempDir()
	// user file overrides the built-in of the same name
	if err := os.WriteFile(filepath.Join(dir, "forgejo.toml"), []byte("mode = \"bearer\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, _ := LoadDefinition(dir, "forgejo"); d.Mode != "bearer" {
		t.Errorf("user override mode = %q, want bearer", d.Mode)
	}
	// a brand-new user-defined connector (zero code)
	if err := os.WriteFile(filepath.Join(dir, "mycorp.toml"), []byte("mode = \"api-key\"\n[env]\nMYCORP = \"http://{broker}\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, _ := LoadDefinition(dir, "mycorp"); d.Mode != "api-key" || d.Env["MYCORP"] != "http://{broker}" {
		t.Errorf("user-defined connector = %+v", d)
	}
	if _, err := LoadDefinition("", "nope"); err == nil {
		t.Error("expected an error for an unknown connector")
	}
}

func TestCredential_BasicUsesUserColonToken(t *testing.T) {
	s := NewStore(t.TempDir())
	conn, err := s.Create("my-forgejo", "forgejo", "http://192.168.1.166:3000", "me", "TOK", "")
	if err != nil {
		t.Fatal(err)
	}
	def, _ := LoadDefinition("", "forgejo")
	cred, err := Credential(conn, def)
	if err != nil {
		t.Fatal(err)
	}
	want := broker.Credential{Mode: broker.ModeBasic, Vendor: "forgejo", Secret: "me:TOK", BaseURL: "http://192.168.1.166:3000"}
	if cred != want {
		t.Errorf("cred = %+v, want %+v", cred, want)
	}
}

func TestCredential_BearerUsesToken(t *testing.T) {
	s := NewStore(t.TempDir())
	conn, _ := s.Create("wp", "woodpecker", "http://wp:8000", "", "WPTOK", "")
	def, _ := LoadDefinition("", "woodpecker")
	cred, _ := Credential(conn, def)
	if cred.Mode != broker.ModeSubscription || cred.Secret != "WPTOK" {
		t.Errorf("cred = %+v, want bearer/WPTOK", cred)
	}
}

func TestWiring_ForgejoGitRewrite(t *testing.T) {
	def, _ := LoadDefinition("", "forgejo")
	cred := broker.Credential{BaseURL: "http://192.168.1.166:3000"}
	env, setup := Wiring(def, cred, "host.containers.internal:9000", "poddle_abc")
	if env != nil {
		t.Errorf("forgejo env should be nil, got %v", env)
	}
	want := `git config --global url."http://poddle_abc:x@host.containers.internal:9000/".insteadOf "http://192.168.1.166:3000/"`
	if len(setup) != 1 || setup[0] != want {
		t.Errorf("setup = %v\nwant %q", setup, want)
	}
}

func TestWiring_WoodpeckerEnv(t *testing.T) {
	def, _ := LoadDefinition("", "woodpecker")
	env, setup := Wiring(def, broker.Credential{}, "host.containers.internal:9000", "poddle_x")
	if setup != nil {
		t.Errorf("woodpecker setup should be nil, got %v", setup)
	}
	if env["WOODPECKER_SERVER"] != "http://host.containers.internal:9000" || env["WOODPECKER_TOKEN"] != "poddle_x" {
		t.Errorf("env = %v", env)
	}
}

func TestStore_CRUD(t *testing.T) {
	s := NewStore(t.TempDir())
	conn, err := s.Create("my-forgejo", "forgejo", "http://forge", "me", "TOK", "")
	if err != nil {
		t.Fatal(err)
	}
	if conn.Owner != "local" {
		t.Errorf("owner default = %q, want local", conn.Owner)
	}
	got, err := s.Get("my-forgejo")
	if err != nil {
		t.Fatal(err)
	}
	if got.Connector != "forgejo" || got.User != "me" || got.BaseURL != "http://forge" {
		t.Errorf("got %+v", got)
	}
	if list, _ := s.List(); len(list) != 1 {
		t.Errorf("list len = %d", len(list))
	}
	if err := s.Remove("my-forgejo"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Errorf("after remove, list = %v", list)
	}
}
