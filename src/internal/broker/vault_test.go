package broker

import (
	"errors"
	"testing"
	"time"
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
	expiresOld := time.Now().Add(time.Hour).Truncate(0)
	id, err := v.Store("local", Credential{
		Mode: ModeOAuthBearer, Secret: "old", RefreshToken: "r0",
		TokenEndpoint: "https://old.example.com/token", ClientID: "client-old",
		ClientSecret: "shh-old", ExpiresAt: expiresOld,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The OAuth metadata (including the sealed ClientSecret) round-trips
	// through Store + Get unchanged.
	got, err := v.Get("local", id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientSecret != "shh-old" {
		t.Errorf("ClientSecret after store: got %q, want %q", got.ClientSecret, "shh-old")
	}
	if got.TokenEndpoint != "https://old.example.com/token" {
		t.Errorf("TokenEndpoint after store: got %q", got.TokenEndpoint)
	}
	if got.ClientID != "client-old" {
		t.Errorf("ClientID after store: got %q", got.ClientID)
	}
	if !got.ExpiresAt.Equal(expiresOld) {
		t.Errorf("ExpiresAt after store: got %v, want %v", got.ExpiresAt, expiresOld)
	}

	expiresNew := time.Now().Add(2 * time.Hour).Truncate(0)
	if err := v.Replace("local", id, Credential{
		Mode: ModeOAuthBearer, Secret: "new", RefreshToken: "r1",
		TokenEndpoint: "https://new.example.com/token", ClientID: "client-new",
		ClientSecret: "shh-new", ExpiresAt: expiresNew,
	}); err != nil {
		t.Fatal(err)
	}
	got, err = v.Get("local", id)
	if err != nil || got.Secret != "new" || got.RefreshToken != "r1" {
		t.Fatalf("after replace: %+v, %v", got, err)
	}
	// Replace must also swap the OAuth metadata, not just Secret/RefreshToken.
	if got.ClientSecret != "shh-new" {
		t.Errorf("ClientSecret after replace: got %q, want %q", got.ClientSecret, "shh-new")
	}
	if got.TokenEndpoint != "https://new.example.com/token" {
		t.Errorf("TokenEndpoint after replace: got %q", got.TokenEndpoint)
	}
	if got.ClientID != "client-new" {
		t.Errorf("ClientID after replace: got %q", got.ClientID)
	}
	if !got.ExpiresAt.Equal(expiresNew) {
		t.Errorf("ExpiresAt after replace: got %v, want %v", got.ExpiresAt, expiresNew)
	}

	if err := v.Replace("local", "nope", Credential{}); err == nil {
		t.Error("Replace of an absent credID must error")
	}
}

func TestVault_EmptyClientSecretRoundTripsToEmpty(t *testing.T) {
	v := NewVault()
	c := Credential{Mode: ModeOAuthBearer, Secret: "tok", TokenEndpoint: "https://example.com/token", ClientID: "cid"}

	id, err := v.Store("local", c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Get("local", id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientSecret != "" {
		t.Errorf("empty ClientSecret should round-trip to \"\", got %q", got.ClientSecret)
	}
}
