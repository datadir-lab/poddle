package broker

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// DefaultHandleTTL is used when IssueHandle is called with ttl <= 0.
const DefaultHandleTTL = 12 * time.Hour

// Handles issues and resolves pod-facing handles, backed by a Vault. A handle
// maps to a (tenant, credID); resolving returns the underlying Credential.
type Handles struct {
	mu      sync.RWMutex
	vault   *Vault
	byValue map[string]Handle
	now     func() time.Time // defaults to time.Now; overridden in tests
}

// NewHandles returns a handle registry over the given vault.
func NewHandles(v *Vault) *Handles {
	return &Handles{vault: v, byValue: map[string]Handle{}, now: time.Now}
}

// IssueHandle mints a revocable, high-entropy handle for (tenant, credID),
// scoped to scope (e.g. a pod name). ttl <= 0 uses DefaultHandleTTL.
func (h *Handles) IssueHandle(tenant, credID, scope string, ttl time.Duration) (Handle, error) {
	val, err := randHandle()
	if err != nil {
		return Handle{}, err
	}
	if ttl <= 0 {
		ttl = DefaultHandleTTL
	}
	handle := Handle{Value: val, Tenant: tenant, CredID: credID, Scope: scope, ExpiresAt: h.now().Add(ttl)}
	h.mu.Lock()
	h.byValue[val] = handle
	h.mu.Unlock()
	return handle, nil
}

// Resolve returns the Credential a handle points to, or ErrNotFound if the
// handle is unknown/revoked or its credential is gone, or ErrExpired if it is
// past its ExpiresAt (in which case the handle record is lazily removed).
func (h *Handles) Resolve(value string) (Credential, error) {
	h.mu.RLock()
	rec, ok := h.byValue[value]
	h.mu.RUnlock()
	if !ok {
		return Credential{}, ErrNotFound
	}
	if !rec.ExpiresAt.IsZero() && !h.now().Before(rec.ExpiresAt) {
		h.mu.Lock()
		delete(h.byValue, value)
		h.mu.Unlock()
		return Credential{}, ErrExpired
	}
	return h.vault.Get(rec.Tenant, rec.CredID)
}

// Revoke invalidates a handle immediately.
func (h *Handles) Revoke(value string) {
	h.mu.Lock()
	delete(h.byValue, value)
	h.mu.Unlock()
}

func randHandle() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "poddle_" + base64.RawURLEncoding.EncodeToString(b), nil
}
