package broker

import (
	"errors"
	"testing"
)

func TestVault_StoreGet(t *testing.T) {
	v := NewVault()
	c := Credential{Mode: ModeSubscription, Vendor: "anthropic", Secret: "tok", BaseURL: "https://api.anthropic.com"}

	id, err := v.Store("local", c)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := v.Get("local", id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != c {
		t.Errorf("got %+v, want %+v", got, c)
	}
}

func TestVault_EmptySecret(t *testing.T) {
	v := NewVault()
	c := Credential{Mode: ModeEndpoint, Vendor: "local", BaseURL: "http://localhost:1234"}

	id, _ := v.Store("local", c)
	got, err := v.Get("local", id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != c {
		t.Errorf("got %+v, want %+v", got, c)
	}
	if got.Secret != "" {
		t.Errorf("empty secret should round-trip to \"\", got %q", got.Secret)
	}
}

func TestVault_CrossTenantDenied(t *testing.T) {
	v := NewVault()
	id, _ := v.Store("tenant-a", Credential{Secret: "a"})

	if _, err := v.Get("tenant-b", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant Get should be ErrNotFound, got %v", err)
	}
}

func TestVault_Delete(t *testing.T) {
	v := NewVault()
	id, _ := v.Store("local", Credential{Secret: "x"})

	v.Delete("local", id)
	if _, err := v.Get("local", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete want ErrNotFound, got %v", err)
	}
}

func TestVault_ReplaceSwapsSecretForSameCredID(t *testing.T) {
	v := NewVault()
	id, err := v.Store("local", Credential{Mode: ModeOAuthBearer, Secret: "old", RefreshToken: "r0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Replace("local", id, Credential{Mode: ModeOAuthBearer, Secret: "new", RefreshToken: "r1"}); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get("local", id)
	if err != nil || got.Secret != "new" || got.RefreshToken != "r1" {
		t.Fatalf("after replace: %+v, %v", got, err)
	}
	if err := v.Replace("local", "nope", Credential{}); err == nil {
		t.Error("Replace of an absent credID must error")
	}
}
