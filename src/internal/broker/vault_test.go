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
