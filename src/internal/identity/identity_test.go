package identity

import (
	"testing"

	"github.com/datadir-lab/poddle/src/internal/broker"
)

// FakeProvider must satisfy the Provider contract.
var _ Provider = (*FakeProvider)(nil)

func TestStore_CreateListGetRemove(t *testing.T) {
	s := NewStore(t.TempDir())

	id, err := s.Create("work", "anthropic")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id.Name != "work" || id.Provider != "anthropic" {
		t.Errorf("id = %+v", id)
	}
	if id.Dir() == "" {
		t.Error("dir empty")
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Name != "work" || list[0].Provider != "anthropic" {
		t.Fatalf("list = %+v", list)
	}

	got, err := s.Get("work")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Provider != "anthropic" || got.Dir() == "" {
		t.Errorf("get = %+v", got)
	}

	if err := s.Remove("work"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Errorf("expected empty after remove, got %+v", list)
	}
}

func TestStore_ListEmpty(t *testing.T) {
	s := NewStore(t.TempDir())
	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("want empty, got %v", list)
	}
}

func TestFakeProvider_Credential(t *testing.T) {
	want := broker.Credential{
		Mode: broker.ModeSubscription, Vendor: "anthropic",
		Secret: "tok", BaseURL: "https://api.anthropic.com",
	}
	f := &FakeProvider{ProviderName: "anthropic", Cred: want}

	got, err := f.Credential(Identity{})
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// Registry + FakeProvider wiring.
func TestRegistry_Get(t *testing.T) {
	reg := Registry{"anthropic": &FakeProvider{ProviderName: "anthropic", Authed: true}}
	p, ok := reg.Get("anthropic")
	if !ok || p.Name() != "anthropic" {
		t.Fatalf("registry get = %v, %v", p, ok)
	}
	if authed, _ := p.IsAuthenticated(Identity{}); !authed {
		t.Error("expected authed")
	}
}
