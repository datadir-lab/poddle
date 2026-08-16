package broker

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func setup() (*Vault, *Handles) {
	v := NewVault()
	return v, NewHandles(v)
}

func TestHandles_IssueResolve(t *testing.T) {
	v, h := setup()
	c := Credential{Mode: ModeSubscription, Vendor: "anthropic", Secret: "tok", BaseURL: "https://api.anthropic.com"}
	id, _ := v.Store("local", c)

	handle, err := h.IssueHandle("local", id, "mybox", 0)
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
	handle, _ := h.IssueHandle("local", id, "mybox", 0)

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
	handle, _ := h.IssueHandle("tenant-a", id, "box", 0)

	v.Delete("tenant-a", id) // underlying cred gone → handle resolves to nothing
	if _, err := h.Resolve(handle.Value); !errors.Is(err, ErrNotFound) {
		t.Errorf("resolve after cred deleted should be ErrNotFound, got %v", err)
	}
}

func TestHandles_ResolvesBeforeExpiry(t *testing.T) {
	v, h := setup()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return base }
	id, _ := v.Store("local", Credential{Secret: "s"})
	handle, _ := h.IssueHandle("local", id, "box", time.Hour)

	if _, err := h.Resolve(handle.Value); err != nil {
		t.Fatalf("resolve before expiry: %v", err)
	}
}

func TestHandles_ExpiredResolve(t *testing.T) {
	v, h := setup()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	id, _ := v.Store("local", Credential{Secret: "s"})
	handle, _ := h.IssueHandle("local", id, "box", time.Hour)

	now = now.Add(2 * time.Hour) // advance past expiry
	if _, err := h.Resolve(handle.Value); !errors.Is(err, ErrExpired) {
		t.Errorf("expired resolve should be ErrExpired, got %v", err)
	}
}

func TestHandles_ExpiredLazyDeleted(t *testing.T) {
	v, h := setup()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }
	id, _ := v.Store("local", Credential{Secret: "s"})
	handle, _ := h.IssueHandle("local", id, "box", time.Hour)

	now = now.Add(2 * time.Hour)
	_, _ = h.Resolve(handle.Value) // triggers lazy delete

	if len(h.byValue) != 0 {
		t.Errorf("expired handle should be removed, byValue has %d entries", len(h.byValue))
	}
	if _, err := h.Resolve(handle.Value); !errors.Is(err, ErrNotFound) {
		t.Errorf("after lazy delete want ErrNotFound, got %v", err)
	}
}

func TestHandles_DefaultTTL(t *testing.T) {
	v, h := setup()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return base }
	id, _ := v.Store("local", Credential{Secret: "s"})

	handle, _ := h.IssueHandle("local", id, "box", 0) // 0 → DefaultHandleTTL
	want := base.Add(DefaultHandleTTL)
	if !handle.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", handle.ExpiresAt, want)
	}
}
