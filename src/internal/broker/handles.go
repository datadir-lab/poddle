package broker

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
)

// Handles issues and resolves pod-facing handles, backed by a Vault. A handle
// maps to a (tenant, credID); resolving returns the underlying Credential.
type Handles struct {
	mu      sync.RWMutex
	vault   *Vault
	byValue map[string]Handle
}

// NewHandles returns a handle registry over the given vault.
func NewHandles(v *Vault) *Handles {
	return &Handles{vault: v, byValue: map[string]Handle{}}
}

// IssueHandle mints a revocable, high-entropy handle for (tenant, credID),
// scoped to scope (e.g. a pod name).
func (h *Handles) IssueHandle(tenant, credID, scope string) (Handle, error) {
	val, err := randHandle()
	if err != nil {
		return Handle{}, err
	}
	handle := Handle{Value: val, Tenant: tenant, CredID: credID, Scope: scope}
	h.mu.Lock()
	h.byValue[val] = handle
	h.mu.Unlock()
	return handle, nil
}

// Resolve returns the Credential a handle points to, or ErrNotFound if the
// handle is unknown/revoked or its credential is gone.
func (h *Handles) Resolve(value string) (Credential, error) {
	h.mu.RLock()
	rec, ok := h.byValue[value]
	h.mu.RUnlock()
	if !ok {
		return Credential{}, ErrNotFound
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
