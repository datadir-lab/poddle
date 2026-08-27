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

// Store seals a credential in the vault and returns its id. If c carries a
// WriteBackKey, this also clears any needs-reauth flag the gateway raised for
// it — a host that just re-stores a fresh credential for a connection has, by
// definition, resolved whatever made the prior refresh fail.
func (b *Broker) Store(c Credential) (string, error) {
	id, err := b.vault.Store(b.tenant, c)
	if err != nil {
		return "", err
	}
	if c.WriteBackKey != "" {
		b.server.gw.clearReauth(c.WriteBackKey)
	}
	return id, nil
}

// IssueHandle mints a pod-facing handle for a stored credential.
func (b *Broker) IssueHandle(credID, scope string, ttl time.Duration) (Handle, error) {
	return b.handles.IssueHandle(b.tenant, credID, scope, ttl)
}

// Revoke invalidates a handle immediately.
func (b *Broker) Revoke(handleValue string) { b.handles.Revoke(handleValue) }

// Resolve returns the credential a handle maps to (used by the L4 broker, which
// reads the handle from a datastore auth exchange rather than an HTTP header).
// The underlying credID is not exposed here — L4 datastore creds are not
// OAuth-refreshed, so callers only need the credential itself.
func (b *Broker) Resolve(handleValue string) (Credential, error) {
	_, c, err := b.handles.Resolve(handleValue)
	return c, err
}

// SetEgressMode configures egress redaction on the gateway: "redact" (default),
// "block", or "off". Call before Serve.
func (b *Broker) SetEgressMode(mode string) { b.server.gw.SetEgressMode(mode) }

// SetAuditor wires the sink that receives one record per proxied request.
func (b *Broker) SetAuditor(a Auditor) { b.server.gw.SetAuditor(a) }

// SetPolicyChecker wires the governance policy checker consulted per request.
func (b *Broker) SetPolicyChecker(pc PolicyChecker) { b.server.gw.SetPolicyChecker(pc) }

// SetLoopbackHost makes a loopback upstream dial the host route instead (a
// containerized broker's host.containers.internal). Empty disables it. Call
// before Serve.
func (b *Broker) SetLoopbackHost(h string) { b.server.gw.SetLoopbackHost(h) }

// EnableOAuthWriteBack turns on durable OAuth refresh-token write-back: when
// the gateway rotates a connection's refresh token, the new material is
// mirrored under dir as <connName>.json, so poddled can reseed it on restart
// without forcing a host reauth. Call before Serve.
func (b *Broker) EnableOAuthWriteBack(dir string) {
	b.server.gw.SetOAuthPersister(NewStateDirPersister(dir))
}

// NeedsReauth returns the WriteBackKeys of connections whose most recent
// OAuth refresh attempt failed — `connect reauth` targets.
func (b *Broker) NeedsReauth() []string { return b.server.gw.NeedsReauth() }

// SCRAMProof delegates the L4 Postgres SCRAM password-bearing step to the
// keeper (see Keeper.SCRAMProof) — the front holds only handle and calls this
// instead of computing the proof from a locally-held password. Exposed on the
// facade so poddled's daemon can wire the L4 Postgres terminator's
// keeper-backed authenticator without reaching into broker internals.
func (b *Broker) SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error) {
	return b.server.gw.keeper.SCRAMProof(handle, salt, iter, authMessage)
}

// Serve starts the injecting gateway and returns the bound address.
func (b *Broker) Serve(addr string) (string, error) { return b.server.Serve(addr) }

// Addr returns the gateway's bound address (empty until Serve).
func (b *Broker) Addr() string { return b.server.Addr() }

// Stop gracefully shuts the gateway down.
func (b *Broker) Stop(ctx context.Context) error { return b.server.Stop(ctx) }
