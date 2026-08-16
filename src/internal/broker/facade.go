package broker

import (
	"context"
	"time"
)

// localTenant is the single tenant used in Phase 1. Multi-tenant is Phase 4.
const localTenant = "local"

// Broker is the composed secretless broker: a Vault holding real credentials, a
// Handles registry issuing pod-facing handles, and a Server exposing the
// injecting gateway. It hides the (single, "local") tenant so callers — i.e.
// `up` — deal only in credentials and handles.
type Broker struct {
	vault   *Vault
	handles *Handles
	server  *Server
	tenant  string
}

// NewBroker wires Vault → Handles → Gateway → Server for the local tenant.
func NewBroker() *Broker {
	v := NewVault()
	h := NewHandles(v)
	return &Broker{
		vault:   v,
		handles: h,
		server:  NewServer(NewGateway(h)),
		tenant:  localTenant,
	}
}

// Store seals a credential in the vault and returns its id.
func (b *Broker) Store(c Credential) (string, error) {
	return b.vault.Store(b.tenant, c)
}

// IssueHandle mints a pod-facing handle for a stored credential.
func (b *Broker) IssueHandle(credID, scope string, ttl time.Duration) (Handle, error) {
	return b.handles.IssueHandle(b.tenant, credID, scope, ttl)
}

// Revoke invalidates a handle immediately.
func (b *Broker) Revoke(handleValue string) { b.handles.Revoke(handleValue) }

// Serve starts the injecting gateway and returns the bound address.
func (b *Broker) Serve(addr string) (string, error) { return b.server.Serve(addr) }

// Addr returns the gateway's bound address (empty until Serve).
func (b *Broker) Addr() string { return b.server.Addr() }

// Stop gracefully shuts the gateway down.
func (b *Broker) Stop(ctx context.Context) error { return b.server.Stop(ctx) }
