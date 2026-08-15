package identity

import (
	"bytes"
	"strings"
	"testing"

	idn "git.dev.datadir.co/datadir/poddle/src/internal/identity"
)

func TestIdentity_AddCreatesAndAuthenticates(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	fake := &idn.FakeProvider{ProviderName: "anthropic"}
	reg := idn.Registry{"anthropic": fake}

	c := NewCmd(store, reg)
	c.SetArgs([]string{"add", "work", "--provider", "anthropic"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !fake.AuthCalled {
		t.Error("expected Authenticate to be called")
	}
	if _, err := store.Get("work"); err != nil {
		t.Errorf("identity not stored: %v", err)
	}
}

func TestIdentity_Ls(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	reg := idn.Registry{"anthropic": &idn.FakeProvider{ProviderName: "anthropic", Authed: true}}

	c := NewCmd(store, reg)
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"ls"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, w := range []string{"NAME", "work", "anthropic", "yes"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("ls output missing %q:\n%s", w, out.String())
		}
	}
}

func TestIdentity_Rm(t *testing.T) {
	store := idn.NewStore(t.TempDir())
	if _, err := store.Create("work", "anthropic"); err != nil {
		t.Fatal(err)
	}
	c := NewCmd(store, idn.Registry{})
	c.SetArgs([]string{"rm", "work"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if list, _ := store.List(); len(list) != 0 {
		t.Errorf("expected removed, got %v", list)
	}
}
