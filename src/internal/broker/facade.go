package broker

import (
	"context"
	"time"
)

// localTenant is the single tenant used in Phase 1. Multi-tenant is Phase 4.
const localTenant = "local"

// Broker is the composed secretless broker: a Custody boundary owning the vault +
// all secret-touching operations, and a Server exposing the injecting gateway. It
// hides the (single, "local") tenant so callers — i.e. `up` — deal only in
// credentials and handles. In Phase-1/default mode Custody is one in-process
// *localKeeper shared with the gateway; in Phase-2 two-process mode it is a
// socketKeeperClient talking to the keeper subprocess (see newBrokerOverKeeper).
type Broker struct {
	custody Custody
	server  *Server
}

// NewBroker wires an in-process keeper (owning Vault → Handles) shared between the
// gateway request path and the facade's control-plane calls, behind a Server.
func NewBroker() *Broker {
	k := newLocalKeeper(NewHandles(NewVault()))
	return newBrokerOverKeeper(k)
}

// newBrokerOverKeeper composes a Broker over an arbitrary Custody, shared between
// the facade and the gateway. In-process this is a *localKeeper; in two-process
// mode it is the socketKeeperClient over the keeper subprocess — the front then
// holds no vault, only the socket.
func newBrokerOverKeeper(c Custody) *Broker {
	return &Broker{custody: c, server: NewServer(newGateway(c))}
}

// Store seals a credential in the vault and returns its id. If c carries a
// WriteBackKey, the keeper also clears any needs-reauth flag raised for it — a
// host that just re-stores a fresh credential for a connection has, by definition,
// resolved whatever made the prior refresh fail.
func (b *Broker) Store(c Credential) (string, error) { return b.custody.Store(c) }

// IssueHandle mints a pod-facing handle for a stored credential.
func (b *Broker) IssueHandle(credID, scope string, ttl time.Duration) (Handle, error) {
	return b.custody.IssueHandle(credID, scope, ttl)
}

// Revoke invalidates a handle immediately.
func (b *Broker) Revoke(handleValue string) { b.custody.Revoke(handleValue) }

// Resolve returns the credential a handle maps to (used by the L4 broker, which
// reads the handle from a datastore auth exchange rather than an HTTP header).
// The underlying credID is not exposed here — L4 datastore creds are not
// OAuth-refreshed, so callers only need the credential itself.
func (b *Broker) Resolve(handleValue string) (Credential, error) {
	return b.custody.ResolveCredential(handleValue)
}

// SetEgressMode configures egress redaction on the gateway: "redact" (default),
// "block", or "off". Call before Serve.
func (b *Broker) SetEgressMode(mode string) { b.custody.SetEgressMode(mode) }

// SetAuditor wires the sink that receives one record per proxied request.
func (b *Broker) SetAuditor(a Auditor) { b.server.gw.SetAuditor(a) }

// SetPolicyChecker wires the governance policy checker consulted per request.
func (b *Broker) SetPolicyChecker(pc PolicyChecker) { b.server.gw.SetPolicyChecker(pc) }

// SetLoopbackHost makes a loopback upstream dial the host route instead (a
// containerized broker's host.containers.internal). Empty disables it. Call
// before Serve.
func (b *Broker) SetLoopbackHost(h string) { b.server.gw.SetLoopbackHost(h) }

// EnableOAuthWriteBack turns on durable OAuth refresh-token write-back: when
// the keeper rotates a connection's refresh token, the new material is mirrored
// under dir as <connName>.json, so poddled can reseed it on restart without
// forcing a host reauth. Call before Serve. In two-process mode this is a client
// no-op — the keeper subprocess configures its own persister at startup, since the
// persister writes to disk keeper-side and can't cross the wire.
func (b *Broker) EnableOAuthWriteBack(dir string) {
	b.custody.SetOAuthPersister(NewStateDirPersister(dir))
}

// NeedsReauth returns the WriteBackKeys of connections whose most recent
// OAuth refresh attempt failed — `connect reauth` targets.
func (b *Broker) NeedsReauth() []string { return b.custody.NeedsReauth() }

// SCRAMProof delegates the L4 Postgres SCRAM password-bearing step to the
// keeper (see Keeper.SCRAMProof) — the front holds only handle and calls this
// instead of computing the proof from a locally-held password. Exposed on the
// facade so poddled's daemon can wire the L4 Postgres terminator's
// keeper-backed authenticator without reaching into broker internals.
func (b *Broker) SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error) {
	return b.custody.SCRAMProof(handle, salt, iter, authMessage)
}

// EnsureCA loads (or creates) the egress-interception CA keeper-side, so the CA
// private key that signs every leaf lives only in the keeper. Call before wiring
// LeafSource into the forward proxy. See Custody.EnsureCA.
func (b *Broker) EnsureCA(dir string) error { return b.custody.EnsureCA(dir) }

// LeafSource returns a LeafSource that mints intercepted-TLS leaves via the keeper
// (Custody.SignLeaf) — the front reassembles each leaf but never holds the CA key.
// Wire it into the forward proxy after a successful EnsureCA.
func (b *Broker) LeafSource() LeafSource { return newCustodyLeafSource(b.custody) }

// Serve starts the injecting gateway and returns the bound address.
func (b *Broker) Serve(addr string) (string, error) { return b.server.Serve(addr) }

// Addr returns the gateway's bound address (empty until Serve).
func (b *Broker) Addr() string { return b.server.Addr() }

// Stop gracefully shuts the gateway down and, in two-process mode, closes the
// keeper socket (which the keeper observes as EOF and exits — so the subprocess
// is terminated on a clean shutdown, not left to the Pdeathsig/EOF-at-exit
// backstop). closeCustody is a no-op for an in-process broker.
func (b *Broker) Stop(ctx context.Context) error {
	err := b.server.Stop(ctx)
	b.closeCustody()
	return err
}

// closeCustody shuts down a two-process Broker's socket client (closing the conn,
// which the keeper observes as EOF and exits). A no-op for an in-process Broker
// (whose custody is a *localKeeper, not a *socketKeeperClient). Platform-agnostic:
// the socketKeeperClient type exists everywhere; only its spawner is Linux-only.
func (b *Broker) closeCustody() {
	if c, ok := b.custody.(*socketKeeperClient); ok {
		_ = c.Close()
	}
}
