package broker

import (
	"errors"
	"strings"
	"testing"
)

func setup() (*Vault, *Handles) {
	v := NewVault()
	return v, NewHandles(v)
}

func TestHandles_IssueResolve(t *testing.T) {
	v, h := setup()
	c := Credential{Mode: ModeSubscription, Vendor: "anthropic", Secret: "tok", BaseURL: "https://api.anthropic.com"}
	id, _ := v.Store("local", c)

	handle, err := h.IssueHandle("local", id, "mybox")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.HasPrefix(handle.Value, "poddle_") {
		t.Errorf("handle value = %q", handle.Value)
	}
	got, err := h.Resolve(handle.Value)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != c {
		t.Errorf("resolved %+v, want %+v", got, c)
	}
}

func TestHandles_RevokedNotFound(t *testing.T) {
	v, h := setup()
	id, _ := v.Store("local", Credential{Secret: "x"})
	handle, _ := h.IssueHandle("local", id, "mybox")

	h.Revoke(handle.Value)
	if _, err := h.Resolve(handle.Value); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked handle should be ErrNotFound, got %v", err)
	}
}

func TestHandles_UnknownNotFound(t *testing.T) {
	_, h := setup()
	if _, err := h.Resolve("poddle_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown handle should be ErrNotFound, got %v", err)
	}
}

func TestHandles_CredGone(t *testing.T) {
	v, h := setup()
	id, _ := v.Store("tenant-a", Credential{Secret: "a"})
	handle, _ := h.IssueHandle("tenant-a", id, "box")

	v.Delete("tenant-a", id) // underlying cred gone → handle resolves to nothing
	if _, err := h.Resolve(handle.Value); !errors.Is(err, ErrNotFound) {
		t.Errorf("resolve after cred deleted should be ErrNotFound, got %v", err)
	}
}
