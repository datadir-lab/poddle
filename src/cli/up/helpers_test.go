package up

import (
	"os"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/connector"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
)

func TestStateVolName(t *testing.T) {
	tests := []struct {
		pod, dir, want string
	}{
		{"mypod", "/home/user/.state", "poddle-mypod-home-user--state"},
		{"Pod1", "A/b", "poddle-Pod1-a-b"},             // dir is lowercased; pod is left as-is
		{"box", "/workspace/", "poddle-box-workspace"}, // surrounding slashes trimmed
		{"box", "café", "poddle-box-caf-"},             // non-ASCII rune -> single '-'
	}
	for _, tt := range tests {
		if got := stateVolName(tt.pod, tt.dir); got != tt.want {
			t.Errorf("stateVolName(%q, %q) = %q, want %q", tt.pod, tt.dir, got, tt.want)
		}
	}
}

func TestRepoEgressHost(t *testing.T) {
	tests := []struct{ repo, want string }{
		{"https://github.com/octocat/Hello-World.git", "github.com"},
		{"http://git.internal:8080/team/app.git", "git.internal"},
		{"git@github.com:octocat/Hello-World.git", ""}, // scp-style SSH — not an HTTP forward-proxy egress
		{"ssh://git@host/repo.git", ""},                // ssh scheme — not http(s)
		{"", ""},
	}
	for _, tt := range tests {
		if got := repoEgressHost(tt.repo); got != tt.want {
			t.Errorf("repoEgressHost(%q) = %q, want %q", tt.repo, got, tt.want)
		}
	}
}

func TestPodL4Addr(t *testing.T) {
	const brokerIP = "10.0.0.9" // the broker's IP on the pod's lock network

	t.Run("cached short-circuits fetch", func(t *testing.T) {
		got, err := podL4Addr("cached:1234", brokerIP, func() (string, error) {
			t.Fatal("fetch should not be called when a cached address is present")
			return "", nil
		})
		if err != nil || got != "cached:1234" {
			t.Fatalf("podL4Addr(cached) = %q, %v; want cached:1234, nil", got, err)
		}
	})

	t.Run("rewrites host to the broker peer IP, keeps port", func(t *testing.T) {
		got, err := podL4Addr("", brokerIP, func() (string, error) { return "127.0.0.1:15432", nil })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "10.0.0.9:15432" {
			t.Errorf("podL4Addr = %q, want 10.0.0.9:15432", got)
		}
	})

	t.Run("fetch error is wrapped", func(t *testing.T) {
		if _, err := podL4Addr("", brokerIP, func() (string, error) { return "", os.ErrClosed }); err == nil {
			t.Error("expected error when fetch fails")
		}
	})

	t.Run("unparseable address is rejected", func(t *testing.T) {
		if _, err := podL4Addr("", brokerIP, func() (string, error) { return "no-port-here", nil }); err == nil {
			t.Error("expected error for an address without a port")
		}
	})
}

// writeToken creates a "<connector>-token" file in the current directory (where
// connector.Credential reads it for a Connection with an empty dir).
func writeToken(t *testing.T, connectorName, token string) {
	t.Helper()
	if err := os.WriteFile(connectorName+"-token", []byte(token), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
}

func TestApplyRedisDatastore(t *testing.T) {
	t.Chdir(t.TempDir())
	writeToken(t, "redis", "realpass")

	conn := connector.Connection{Name: "cache", Connector: "redis", BaseURL: "redis://realhost:6379"}
	def := connector.Definition{Transport: "l4-redis"}

	var spec sandbox.Spec
	if err := applyRedisDatastore(stubBroker{}, conn, def, "mypod", "10.0.0.1:16379", nil, &spec); err != nil {
		t.Fatalf("applyRedisDatastore: %v", err)
	}

	// The pod is pointed at the L4 broker with the handle as its password; the
	// real credential (realpass) never reaches the pod env.
	want := map[string]string{
		"REDIS_HOST":     "10.0.0.1",
		"REDIS_PORT":     "16379",
		"REDIS_PASSWORD": "poddle_stub", // the handle from stubBroker.IssueHandle
		"REDIS_URL":      "redis://:poddle_stub@10.0.0.1:16379",
	}
	for k, v := range want {
		if spec.Env[k] != v {
			t.Errorf("spec.Env[%q] = %q, want %q", k, spec.Env[k], v)
		}
	}
}

func TestApplyPostgresDatastore(t *testing.T) {
	t.Chdir(t.TempDir())
	writeToken(t, "postgres", "realpass")

	conn := connector.Connection{
		Name:      "db",
		Connector: "postgres",
		BaseURL:   "postgres://appuser@pghost:5432/appdb",
		User:      "appuser",
	}
	def := connector.Definition{Transport: "l4-postgres"}

	var spec sandbox.Spec
	if err := applyPostgresDatastore(stubBroker{}, conn, def, "mypod", "10.0.0.1:15432", nil, &spec); err != nil {
		t.Fatalf("applyPostgresDatastore: %v", err)
	}

	want := map[string]string{
		"PGHOST":     "10.0.0.1",
		"PGPORT":     "15432",
		"PGPASSWORD": "poddle_stub", // the handle
		"PGUSER":     "appuser",
		"PGDATABASE": "appdb",
	}
	for k, v := range want {
		if spec.Env[k] != v {
			t.Errorf("spec.Env[%q] = %q, want %q", k, spec.Env[k], v)
		}
	}
}

func TestApplyPostgresDatastore_NoUserNoDatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	writeToken(t, "postgres", "realpass")

	// No User and a base URL without a path: PGUSER and PGDATABASE stay unset.
	conn := connector.Connection{Name: "db", Connector: "postgres", BaseURL: "postgres://pghost:5432"}
	def := connector.Definition{Transport: "l4-postgres"}

	var spec sandbox.Spec
	if err := applyPostgresDatastore(stubBroker{}, conn, def, "mypod", "10.0.0.1:15432", nil, &spec); err != nil {
		t.Fatalf("applyPostgresDatastore: %v", err)
	}
	if _, ok := spec.Env["PGUSER"]; ok {
		t.Errorf("PGUSER should be unset, got %q", spec.Env["PGUSER"])
	}
	if _, ok := spec.Env["PGDATABASE"]; ok {
		t.Errorf("PGDATABASE should be unset, got %q", spec.Env["PGDATABASE"])
	}
}

func TestApplyRedisDatastore_MissingTokenErrors(t *testing.T) {
	t.Chdir(t.TempDir()) // no token file written

	conn := connector.Connection{Name: "cache", Connector: "redis", BaseURL: "redis://realhost:6379"}
	def := connector.Definition{Transport: "l4-redis"}

	var spec sandbox.Spec
	if err := applyRedisDatastore(stubBroker{}, conn, def, "mypod", "10.0.0.1:16379", nil, &spec); err == nil {
		t.Error("expected an error when the connection token is missing")
	}
}
