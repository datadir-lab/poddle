package anthropic

import (
	"os"
	"path/filepath"
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/identity"
)

// Provider must satisfy the identity.Provider contract.
var _ identity.Provider = (*Provider)(nil)

func testIdentity(t *testing.T) identity.Identity {
	t.Helper()
	id, err := identity.NewStore(t.TempDir()).Create("work", "anthropic")
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	return id
}

func TestIsAuthenticated_FalseWithoutToken(t *testing.T) {
	ok, err := New().IsAuthenticated(testIdentity(t))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("want false when no token stored")
	}
}

func TestIsAuthenticated_TrueWithToken(t *testing.T) {
	p := New()
	id := testIdentity(t)
	if err := os.WriteFile(filepath.Join(id.Dir(), tokenFile), []byte("tok-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := p.IsAuthenticated(id); err != nil || !ok {
		t.Errorf("IsAuthenticated = %v, %v; want true, nil", ok, err)
	}
}

func TestCredential_FromStoredToken(t *testing.T) {
	p := New()
	id := testIdentity(t)
	if err := os.WriteFile(filepath.Join(id.Dir(), tokenFile), []byte("tok-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := p.Credential(id)
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	want := broker.Credential{
		Mode: broker.ModeSubscription, Vendor: "anthropic",
		Secret: "tok-123", BaseURL: "https://api.anthropic.com",
	}
	if c != want {
		t.Errorf("got %+v, want %+v", c, want)
	}
}

func TestCredential_ErrorsWithoutToken(t *testing.T) {
	if _, err := New().Credential(testIdentity(t)); err == nil {
		t.Error("expected an error when no token is stored")
	}
}
