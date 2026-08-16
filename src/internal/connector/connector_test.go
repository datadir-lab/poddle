package connector

import (
	"os"
	"path/filepath"
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
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

func TestBuiltins_AllPresent(t *testing.T) {
	for _, name := range []string{
		"forgejo", "gitea", "github", "gitlab", "bitbucket",
		"woodpecker", "drone", "argocd", "jenkins",
		"npm", "pypi", "docker",
	} {
		if _, err := LoadDefinition("", name); err != nil {
			t.Errorf("built-in connector %q missing: %v", name, err)
		}
	}
}

func TestWiring_JenkinsURLHandle(t *testing.T) {
	def, _ := LoadDefinition("", "jenkins")
	env, setup := Wiring(def, broker.Credential{}, "host.containers.internal:9000", "poddle_j")
	if setup != nil {
		t.Errorf("jenkins setup should be nil, got %v", setup)
	}
	if env["JENKINS_URL"] != "http://poddle_j:x@host.containers.internal:9000" {
		t.Errorf("jenkins env = %v", env)
	}
}

func TestWiring_ArgocdEnv(t *testing.T) {
	def, _ := LoadDefinition("", "argocd")
	env, _ := Wiring(def, broker.Credential{}, "host.containers.internal:9000", "poddle_a")
	if env["ARGOCD_SERVER"] != "host.containers.internal:9000" ||
		env["ARGOCD_AUTH_TOKEN"] != "poddle_a" || env["ARGOCD_OPTS"] != "--plaintext" {
		t.Errorf("argocd env = %v", env)
	}
}

func TestWiring_DockerConfigJSON(t *testing.T) {
	def, _ := LoadDefinition("", "docker")
	env, setup := Wiring(def, broker.Credential{}, "host.containers.internal:9000", "poddle_k")
	if env != nil {
		t.Errorf("docker env should be nil: %v", env)
	}
	want := `mkdir -p ~/.docker && printf '{"auths":{"%s":{"auth":"%s"}}}\n' "host.containers.internal:9000" "$(printf '%s:x' "poddle_k" | base64 | tr -d '\n')" > ~/.docker/config.json`
	if len(setup) != 1 || setup[0] != want {
		t.Errorf("docker setup = %v\nwant %q", setup, want)
	}
}

func TestWiring_DroneEnv(t *testing.T) {
	def, _ := LoadDefinition("", "drone")
	env, setup := Wiring(def, broker.Credential{}, "host.containers.internal:9000", "poddle_d")
	if setup != nil {
		t.Errorf("drone setup should be nil, got %v", setup)
	}
	if env["DRONE_SERVER"] != "http://host.containers.internal:9000" || env["DRONE_TOKEN"] != "poddle_d" {
		t.Errorf("drone env = %v", env)
	}
}

func TestWiring_PypiIndexURL(t *testing.T) {
	def, _ := LoadDefinition("", "pypi")
	env, setup := Wiring(def, broker.Credential{BaseURL: "https://pypi.org"}, "host.containers.internal:9000", "poddle_p")
	if env != nil {
		t.Errorf("pypi env should be nil: %v", env)
	}
	want := `pip config set global.index-url http://poddle_p:x@host.containers.internal:9000/simple/`
	if len(setup) != 1 || setup[0] != want {
		t.Errorf("pypi setup = %v\nwant %q", setup, want)
	}
}

func TestCredential_PypiTokenUser(t *testing.T) {
	s := NewStore(t.TempDir())
	conn, _ := s.Create("pp", "pypi", "", "__token__", "pypi-AgEI", "") // no --url → default
	def, _ := LoadDefinition("", "pypi")
	cred, _ := Credential(conn, def)
	if cred.Mode != broker.ModeBasic || cred.Secret != "__token__:pypi-AgEI" || cred.BaseURL != "https://pypi.org" {
		t.Errorf("pypi cred = %+v", cred)
	}
}

func TestCredential_DefaultBaseURL(t *testing.T) {
	s := NewStore(t.TempDir())
	conn, _ := s.Create("gh", "github", "", "me", "PAT", "") // no --url → default
	def, _ := LoadDefinition("", "github")
	cred, _ := Credential(conn, def)
	if cred.Mode != broker.ModeBasic || cred.Secret != "me:PAT" || cred.BaseURL != "https://github.com" {
		t.Errorf("github cred = %+v", cred)
	}
}

func TestCredential_ConnectionURLOverridesDefault(t *testing.T) {
	s := NewStore(t.TempDir())
	conn, _ := s.Create("ghe", "github", "https://github.mycorp.com", "me", "PAT", "")
	def, _ := LoadDefinition("", "github")
	cred, _ := Credential(conn, def)
	if cred.BaseURL != "https://github.mycorp.com" {
		t.Errorf("connection URL should override the default, got %q", cred.BaseURL)
	}
}

func TestWiring_GithubGitRewrite(t *testing.T) {
	def, _ := LoadDefinition("", "github")
	_, setup := Wiring(def, broker.Credential{BaseURL: "https://github.com"}, "host.containers.internal:9000", "poddle_y")
	want := `git config --global url."http://poddle_y:x@host.containers.internal:9000/".insteadOf "https://github.com/"`
	if len(setup) != 1 || setup[0] != want {
		t.Errorf("github wiring = %v", setup)
	}
}

func TestWiring_NpmRegistryAndToken(t *testing.T) {
	def, _ := LoadDefinition("", "npm")
	env, setup := Wiring(def, broker.Credential{BaseURL: "https://registry.npmjs.org"}, "host.containers.internal:9000", "poddle_x")
	if env != nil {
		t.Errorf("npm env should be nil: %v", env)
	}
	want := []string{
		"npm config set registry http://host.containers.internal:9000/",
		"npm config set //host.containers.internal:9000/:_authToken poddle_x",
	}
	if len(setup) != 2 || setup[0] != want[0] || setup[1] != want[1] {
		t.Errorf("npm setup = %v\nwant %v", setup, want)
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
