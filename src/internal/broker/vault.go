package broker

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/awnumar/memguard"
)

// ErrNotFound is returned when a credential (or handle) does not resolve —
// including cross-tenant access, which is treated as "not found".
var ErrNotFound = errors.New("broker: not found")

// ErrExpired is returned when a handle is past its ExpiresAt. The gateway maps
// it to the same 401 as ErrNotFound; it stays distinct for logging.
var ErrExpired = errors.New("broker: handle expired")

// vaultEntry holds a credential's non-secret fields plus its secret(s) sealed in
// memguard Enclaves (encrypted + page-locked in memory). secret is nil when the
// credential has no secret (e.g. an open local endpoint); refresh is nil for
// non-OAuth credentials.
type vaultEntry struct {
	mode          Mode
	vendor        string
	baseURL       string
	secret        *memguard.Enclave
	refresh       *memguard.Enclave // nil for non-OAuth
	expiresAt     time.Time
	tokenEndpoint string
	clientID      string
	clientSecret  string
}

// Vault holds credentials in memory, scoped by tenant. Secrets are sealed in
// memguard enclaves — never held as a plain map value, written to disk, or
// handed to a pod. See Get for the one unavoidable plaintext boundary.
type Vault struct {
	mu    sync.RWMutex
	creds map[string]vaultEntry // key = tenant "/" credID
}

// NewVault returns an empty in-memory vault.
func NewVault() *Vault {
	return &Vault{creds: map[string]vaultEntry{}}
}

func vaultKey(tenant, credID string) string { return tenant + "/" + credID }

// sealEntry builds a vaultEntry from c, sealing c.Secret and (when non-empty)
// c.RefreshToken into memguard enclaves and copying across the non-secret OAuth
// metadata. Used by both Store and Replace so the sealing logic lives in one
// place.
func sealEntry(c Credential) vaultEntry {
	e := vaultEntry{
		mode: c.Mode, vendor: c.Vendor, baseURL: c.BaseURL,
		expiresAt: c.ExpiresAt, tokenEndpoint: c.TokenEndpoint,
		clientID: c.ClientID, clientSecret: c.ClientSecret,
	}
	if c.Secret != "" {
		e.secret = memguard.NewEnclave([]byte(c.Secret))
	}
	if c.RefreshToken != "" {
		e.refresh = memguard.NewEnclave([]byte(c.RefreshToken))
	}
	return e
}

// Store saves a credential for a tenant and returns a generated credID. The
// secret (and, for OAuth, the refresh token) is sealed into an enclave; the
// []byte copy taken here is wiped by memguard once sealed. (The caller's
// original string persists until GC — an unavoidable boundary, same as Get.)
func (v *Vault) Store(tenant string, c Credential) (string, error) {
	id, err := randID()
	if err != nil {
		return "", err
	}
	e := sealEntry(c)
	v.mu.Lock()
	v.creds[vaultKey(tenant, id)] = e
	v.mu.Unlock()
	return id, nil
}

// Replace swaps the sealed material for an existing credID in place, so a
// handle bound to that credID keeps resolving — used to store a refreshed
// OAuth token. It errors if credID is not present for tenant.
func (v *Vault) Replace(tenant, credID string, c Credential) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	key := vaultKey(tenant, credID)
	if _, ok := v.creds[key]; !ok {
		return fmt.Errorf("vault: unknown credential %q", credID)
	}
	v.creds[key] = sealEntry(c)
	return nil
}

// Get returns the credential for (tenant, credID); cross-tenant access fails
// with ErrNotFound. The returned Credential.Secret is a transient plaintext
// copy (unavoidable at the net/http boundary); the sealed enclave is untouched
// and the decrypted LockedBuffer is destroyed before returning.
func (v *Vault) Get(tenant, credID string) (Credential, error) {
	v.mu.RLock()
	e, ok := v.creds[vaultKey(tenant, credID)]
	v.mu.RUnlock()
	if !ok {
		return Credential{}, ErrNotFound
	}
	c := Credential{
		Mode: e.mode, Vendor: e.vendor, BaseURL: e.baseURL,
		ExpiresAt: e.expiresAt, TokenEndpoint: e.tokenEndpoint,
		ClientID: e.clientID, ClientSecret: e.clientSecret,
	}
	if e.secret != nil {
		lb, err := e.secret.Open()
		if err != nil {
			return Credential{}, err
		}
		c.Secret = string(lb.Bytes()) // string(...) copies out before we destroy the buffer
		lb.Destroy()
	}
	if e.refresh != nil {
		lb, err := e.refresh.Open()
		if err != nil {
			return Credential{}, err
		}
		c.RefreshToken = string(lb.Bytes()) // string(...) copies out before we destroy the buffer
		lb.Destroy()
	}
	return c, nil
}

// Delete removes a credential. The enclave's encrypted bytes are freed by GC.
func (v *Vault) Delete(tenant, credID string) {
	v.mu.Lock()
	delete(v.creds, vaultKey(tenant, credID))
	v.mu.Unlock()
}

// Purge wipes all memguard-protected memory. Wire it into process shutdown.
func Purge() { memguard.Purge() }

func randID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
