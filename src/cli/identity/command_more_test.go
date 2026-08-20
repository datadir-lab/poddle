package identity

import (
	"bytes"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
)

func mustCreate(t *testing.T, s *idn.Store, name, provider string) {
	t.Helper()
	if _, err := s.Create(name, provider); err != nil {
		t.Fatal(err)
	}
}

func runIdentity(t *testing.T, store *idn.Store, reg idn.Registry, args ...string) (string, error) {
	t.Helper()
	c := NewCmd(&app.App{Identities: store, Providers: reg})
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SilenceUsage = true
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), err
}

func TestIdentity_Status_Authenticated(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	mustCreate(t, store, "work", "anthropic")
	reg := idn.Registry{"anthropic": &idn.FakeProvider{ProviderName: "anthropic", Authed: true}}

	out, err := runIdentity(t, store, reg, "status", "work")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "authenticated") || strings.Contains(out, "NOT authenticated") {
		t.Errorf("status output = %q, want 'authenticated'", out)
	}
}

func TestIdentity_Status_NotAuthenticated(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	mustCreate(t, store, "work", "anthropic")
	reg := idn.Registry{"anthropic": &idn.FakeProvider{ProviderName: "anthropic", Authed: false}}

	out, err := runIdentity(t, store, reg, "status", "work")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "NOT authenticated") {
		t.Errorf("status output = %q, want 'NOT authenticated'", out)
	}
}

func TestIdentity_Status_UnknownIdentity(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := runIdentity(t, store, idn.Registry{}, "status", "ghost"); err == nil {
		t.Error("expected an error for an unknown identity")
	}
}

func TestIdentity_Status_UnknownProvider(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	mustCreate(t, store, "work", "mystery") // provider not in the registry
	if _, err := runIdentity(t, store, idn.Registry{}, "status", "work"); err == nil {
		t.Error("expected an error for an unknown provider")
	}
}

func TestIdentity_Add_UnknownProvider(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := runIdentity(t, store, idn.Registry{}, "add", "work", "--provider", "nope"); err == nil {
		t.Error("expected an error for an unknown provider")
	}
}

func TestIdentity_Ls_UnknownProviderShowsQuestionMark(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	mustCreate(t, store, "work", "gone") // provider absent from the registry -> authedLabel "?"

	out, err := runIdentity(t, store, idn.Registry{}, "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(out, "?") {
		t.Errorf("ls should mark an unknown-provider identity with '?', got:\n%s", out)
	}
}
