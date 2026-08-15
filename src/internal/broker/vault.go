package broker

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
)

// ErrNotFound is returned when a credential (or handle) does not resolve —
// including cross-tenant access, which is treated as "not found".
var ErrNotFound = errors.New("broker: not found")

// Vault holds credentials in memory, scoped by tenant. A credential is never
// written to disk or handed to a pod.
type Vault struct {
	mu    sync.RWMutex
	creds map[string]Credential // key = tenant "/" credID
}

// NewVault returns an empty in-memory vault.
func NewVault() *Vault {
	return &Vault{creds: map[string]Credential{}}
}

func vaultKey(tenant, credID string) string { return tenant + "/" + credID }

// Store saves a credential for a tenant and returns a generated credID.
func (v *Vault) Store(tenant string, c Credential) (string, error) {
	id, err := randID()
	if err != nil {
		return "", err
	}
	v.mu.Lock()
	v.creds[vaultKey(tenant, id)] = c
	v.mu.Unlock()
	return id, nil
}

// Get returns the credential for (tenant, credID); cross-tenant access fails
// with ErrNotFound.
func (v *Vault) Get(tenant, credID string) (Credential, error) {
	v.mu.RLock()
	c, ok := v.creds[vaultKey(tenant, credID)]
	v.mu.RUnlock()
	if !ok {
		return Credential{}, ErrNotFound
	}
	return c, nil
}

// Delete removes a credential.
func (v *Vault) Delete(tenant, credID string) {
	v.mu.Lock()
	delete(v.creds, vaultKey(tenant, credID))
	v.mu.Unlock()
}

func randID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
