package broker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/datadir-lab/poddle/src/internal/l4"
	"github.com/datadir-lab/poddle/src/internal/oauth"
	"github.com/datadir-lab/poddle/src/internal/tlsca"
)

// refreshSkew is how far before an OAuth access token's ExpiresAt the keeper
// proactively refreshes it, so an in-flight request isn't injected with a token
// that expires mid-flight.
const refreshSkew = 60 * time.Second

// minForcedRefreshInterval rate-limits reactive (forced) OAuth rotation per
// credential. A legitimate reactive-retry forces at most one refresh per request
// (and genuine repeat early-revocations of one credential seconds apart are
// implausible — the provider just issued the token), so this never bites real
// traffic; it caps a compromised FRONT that loops ForceReinject to churn the
// refresh token and hammer the provider's token endpoint (privsep audit I1). The
// proactive path (refreshIfStale) needs no such cap: it only rotates a token that
// is actually near expiry, so it can't be looped.
const minForcedRefreshInterval = 30 * time.Second

// PublicCred is the non-secret view of a credential the gateway FRONT is allowed
// to hold: the auth mode (to decide whether to buffer the body and run the OAuth
// reactive-retry path), the vendor label, and the upstream base URL (to dial and
// to policy-check the hostname). It carries NO secret field — the front never
// holds the access token, the refresh token, or the OAuth client secret.
type PublicCred struct {
	Mode    Mode
	Vendor  string
	BaseURL string
}

// HeaderMutation is an auth-injection result expressed as serializable data
// instead of an in-place mutation of a live http.Header (which cannot cross the
// Phase-2 keeper/front process boundary). Apply performs the same Del-then-Set
// the former applyAuth did directly on the request header: the front applies it
// to the outbound request, so the real secret lands in the request header only
// transiently — the front never holds a Credential. Delete is applied before
// Set (a key that appears in both ends up Set — matching applyAuth's Del-then-Set
// for X-Goog-Api-Key).
type HeaderMutation struct {
	Delete []string          // header keys to remove (handle + any stale auth key)
	Set    map[string]string // header key -> real secret value to set
}

// Apply mutates h per the mutation: every Delete key removed, then every Set key
// written. A zero HeaderMutation (both nil) is a no-op.
func (m HeaderMutation) Apply(h http.Header) {
	for _, k := range m.Delete {
		h.Del(k)
	}
	for k, v := range m.Set {
		h.Set(k, v)
	}
}

// Keeper is the privilege boundary between the gateway FRONT (request parsing +
// reverse proxy, which must never hold a plaintext Credential) and credential
// custody + every secret-bearing operation. In Phase 1 it is satisfied in-process
// by *localKeeper; Phase 2 moves it behind a socketpair to a separate vault
// process (see docs/design/broker-privilege-separation.md) with no change to the
// front — the method set is already RPC-shaped: pure (inputs) → result calls.
//
// The front holds only opaque, non-secret values across this boundary: a credID
// (a stable lookup/lock key), a PublicCred, and a fingerprint — a truncated hash
// of the injected access token, never the token itself.
type Keeper interface {
	// Resolve maps a pod handle to its credID and non-secret PublicCred, or an
	// error (unknown/revoked/expired handle) which the front turns into a Basic
	// 401 challenge. The plaintext Credential never crosses back to the front.
	Resolve(handle string) (credID string, pub PublicCred, err error)

	// InjectAuth refreshes a stale OAuth access token (persisting any rotation),
	// then returns the HeaderMutation that injects the REAL secret per the
	// credential's mode (deleting the pod handle header first) for the front to
	// Apply, plus an opaque, non-secret fingerprint of the injected access token;
	// the front holds the fingerprint, never the token. A non-nil err (refresh
	// failure) maps to the front's fail-closed bare 401.
	InjectAuth(ctx context.Context, handle, credID string) (mut HeaderMutation, fingerprint string, err error)

	// ForceReinject is the reactive-retry path (an upstream 401 on a token that
	// wasn't yet stale). Under the per-credID lock it re-reads the live credential
	// and, if a peer already rotated it (the live secret's fingerprint no longer
	// matches the rejected one), reuses that peer's token WITHOUT a second rotation
	// — preserving the refresh-token-family revocation collapse. Otherwise it
	// force-refreshes. Either way it returns the HeaderMutation that injects the
	// resulting secret for the front to Apply. A non-nil err maps to the front's
	// fail-closed bare 401 (reauth already flagged keeper-side).
	ForceReinject(ctx context.Context, handle, credID, rejectedFingerprint string) (mut HeaderMutation, err error)

	// RedactBody scans a textual egress body (passed as bytes — no live http
	// object, so it is boundary-safe) for the handle's managed secret(s) and
	// returns the scrubbed body, whether egress must be BLOCKED (block mode + a
	// hit), and the hit count. It lives keeper-side because it needs the sealed
	// managed secret to scan; the front applies the non-secret textual/size gate
	// before calling and turns blocked into a 403 (not forwarding) after.
	//
	// Call order is load-bearing: the caller must run InjectAuth (which calls
	// refreshIfStale) for handle BEFORE RedactBody, so a just-rotated credential
	// is already persisted to the vault when RedactBody re-resolves handle — the
	// scan then targets the SAME secret that's about to hit the wire, not a
	// stale pre-rotation one.
	RedactBody(handle string, body []byte) (scrubbed []byte, blocked bool, hits int)

	// NeedsReauth, ClearReauth, and FlagReauth manage the set of connections whose
	// most recent OAuth refresh attempt failed (surfaced to operators via
	// `connect reauth`). ClearReauth is keyed by WriteBackKey (the host clears it
	// on re-store); FlagReauth takes a handle (the front holds no WriteBackKey) and
	// the keeper resolves it to the key.
	NeedsReauth() []string
	ClearReauth(key string)
	FlagReauth(handle string)

	// SCRAMProof is the SCRAM password-bearing step for an L4 Postgres session
	// (the l4.scramAuthenticator shape, keyed by the pod handle that session
	// authenticated with): the one step of a SCRAM exchange that needs the real
	// password. The L4 Postgres terminator holds only handle and a
	// l4.keeperSCRAMAuthenticator that calls this instead of computing the proof
	// itself, so the DB password never becomes long-lived state in the L4 front —
	// the same custody rule InjectAuth/RedactBody already apply to the HTTP path.
	// Today the call is in-process (*localKeeper); under Tier 2 it crosses the
	// socketpair to the vault process with no change to l4's state machine.
	SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error)

	// SetEgressMode and SetOAuthPersister configure keeper-owned state (the egress
	// redactor and the OAuth write-back sink). Called before Serve, so the facade's
	// public setters keep working now that these fields live keeper-side.
	SetEgressMode(mode string)
	SetOAuthPersister(p OAuthPersister)
}

// Custody is the FULL secret-custody boundary the keeper process owns: the
// per-request Keeper interface (the gateway path) PLUS the control-plane mutations
// the Broker facade performs when the host stores/issues/revokes credentials, and
// the full-credential resolve the L4 datastore path needs. In Phase-2 two-process
// mode this whole interface crosses the socketpair to the keeper process (which
// then holds the ONLY copy of the vault); in-process it is one *localKeeper. The
// per-request Keeper subset is what the Gateway alone consumes.
type Custody interface {
	Keeper

	// Store seals a credential in the vault and returns its id, also clearing any
	// needs-reauth flag for its WriteBackKey (re-storing a fresh credential resolves
	// whatever made a prior refresh fail). The credential (with its secret) crosses
	// FROM the host-facing front TO the keeper here — the front does not retain it;
	// the vault copy lives only keeper-side.
	Store(c Credential) (credID string, err error)

	// IssueHandle mints a pod-facing handle for a stored credential.
	IssueHandle(credID, scope string, ttl time.Duration) (Handle, error)

	// Revoke invalidates a handle immediately.
	Revoke(handleValue string)

	// ResolveDatastore resolves a handle to its L4 datastore connection target
	// (address + user + password + db), parsed from the credential's DSN
	// KEEPER-SIDE. It returns ONLY those datastore fields — never the full
	// Credential — so an OAuth credential's refresh token and client secret can
	// never cross to the untrusted front (only the datastore password does, which
	// the L4 front needs to make the connection). A non-datastore credential (whose
	// BaseURL is not a datastore DSN) yields an error, so a compromised front can't
	// use this to read the OAuth material of an HTTP credential either. Distinct
	// from Keeper.Resolve, which returns only the non-secret PublicCred for the HTTP
	// gateway.
	ResolveDatastore(handleValue string) (l4.Target, error)

	// EnsureCA loads (or creates + persists) the egress-interception CA under dir,
	// keeper-side — so the CA PRIVATE KEY that signs every leaf lives only in the
	// keeper, never the front. Eager: the daemon calls it when the forward proxy
	// starts, so the CA cert file exists before `up` injects it into a pod's trust
	// store. Idempotent.
	EnsureCA(dir string) error

	// SignLeaf mints a per-host leaf certificate signed by the keeper's CA and
	// returns it as a serializable DER cert + PKCS#8 key; the front reassembles a
	// tls.Certificate (tlsca.LeafFromDER) to terminate the intercepted TLS
	// handshake. Only the per-host leaf key crosses the boundary — the CA key that
	// signs it stays keeper-side. Errors if EnsureCA has not run.
	SignLeaf(host string) (certDER, keyDER []byte, err error)
}

// Store seals a credential in the vault (single "local" tenant) and clears any
// needs-reauth flag for its WriteBackKey. Moved from the Broker facade so that in
// two-process mode the seal + the reauth-clear both happen keeper-side, where the
// vault and the needs-reauth set live.
func (k *localKeeper) Store(c Credential) (string, error) {
	id, err := k.handles.vault.Store(localTenant, c)
	if err != nil {
		return "", err
	}
	if c.WriteBackKey != "" {
		k.ClearReauth(c.WriteBackKey)
	}
	return id, nil
}

// IssueHandle mints a pod-facing handle for a stored credential (single "local"
// tenant).
func (k *localKeeper) IssueHandle(credID, scope string, ttl time.Duration) (Handle, error) {
	return k.handles.IssueHandle(localTenant, credID, scope, ttl)
}

// Revoke invalidates a handle immediately.
func (k *localKeeper) Revoke(handleValue string) { k.handles.Revoke(handleValue) }

// ResolveDatastore resolves a handle to its L4 datastore Target, parsing the DSN
// keeper-side so only {Addr,User,Pass,DB} — not the OAuth refresh token / client
// secret — can cross to the front. A non-datastore credential's BaseURL fails the
// DSN parse and errors, so this can't be used to read HTTP/OAuth credentials.
func (k *localKeeper) ResolveDatastore(handleValue string) (l4.Target, error) {
	_, c, err := k.handles.Resolve(handleValue)
	if err != nil {
		return l4.Target{}, err
	}
	// Only datastore credentials (redis://, postgres://, …) resolve to a target.
	// Refuse an HTTP/OAuth credential so a compromised front can't use this path to
	// probe HTTP-credential state — the OAuth refresh token / client secret live in
	// the Credential's own fields (never in BaseURL) and can't be reached through
	// l4.Target anyway, but this makes the boundary explicit. url.Parse lowercases
	// the scheme, so the check is case-insensitive (HTTPS:// too).
	if u, perr := url.Parse(c.BaseURL); perr != nil || u.Scheme == "" || u.Scheme == "http" || u.Scheme == "https" {
		return l4.Target{}, errors.New("resolve-datastore: not a datastore credential")
	}
	return l4.TargetFromDSN(c.BaseURL)
}

// EnsureCA loads (or creates + persists) the egress-interception CA under dir and
// holds it keeper-side. Idempotent — a later call reloads (e.g. after a rotation).
func (k *localKeeper) EnsureCA(dir string) error {
	a, err := tlsca.Load(dir)
	if err != nil {
		return err
	}
	k.caAuth.Store(a)
	return nil
}

// SignLeaf mints a per-host leaf signed by the keeper's CA. Errors if EnsureCA has
// not populated the CA.
func (k *localKeeper) SignLeaf(host string) ([]byte, []byte, error) {
	a := k.caAuth.Load()
	if a == nil {
		return nil, nil, errors.New("keeper: egress CA not loaded")
	}
	return a.SignLeafDER(host)
}

var _ Custody = (*localKeeper)(nil)

// localKeeper satisfies Keeper in-process. It owns the handle registry (the vault
// access path), the OAuth refresh hook and its per-credID serialization, the
// egress redactor, the write-back persister, and the needs-reauth set — i.e. all
// the credential-custody and secret-touching state that used to sit on Gateway.
// Every refresh/rotate/redact/reauth behavior below is moved verbatim from the
// former Gateway; the fingerprint hashing is the only logic new to the refactor.
type localKeeper struct {
	handles *Handles
	// redactor is an atomic.Pointer because SetEgressMode swaps it while RedactBody
	// reads it, and under Tier-2 the keeper serves both concurrently (serveKeeper
	// dispatches every method in its own goroutine) — the old plain-pointer field
	// assumed single-threaded config-before-serve, which no longer holds.
	redactor atomic.Pointer[Redactor]

	// refresh mints a fresh Credential from a stale ModeOAuthBearer one (default
	// calls oauth.Refresh; tests override it). refMu guards refLocks, a per-credID
	// mutex map that serializes concurrent refreshes of one credential so N racing
	// requests trigger exactly one token request.
	refresh  func(context.Context, Credential) (Credential, error)
	refMu    sync.Mutex
	refLocks map[string]*sync.Mutex
	// lastForced records the last reactive (forced) rotation time per credID, so
	// forceRefresh can rate-limit it (minForcedRefreshInterval) against a compromised
	// front looping ForceReinject. Guarded by refMu.
	lastForced map[string]time.Time

	// persister durably mirrors a connection's rotated OAuth refresh token to disk
	// (nil = no write-back). reauthMu guards needsReauth, the set of WriteBackKeys
	// whose most recent refresh attempt failed — surfaced via NeedsReauth() and
	// cleared once the host re-stores a fresh credential (Broker.Store).
	persister   OAuthPersister
	reauthMu    sync.Mutex
	needsReauth map[string]bool

	// caAuth is the egress-interception CA (holds the CA private key that signs
	// every leaf). An atomic.Pointer because EnsureCA stores it while concurrent
	// SignLeaf calls load it. nil until EnsureCA runs (interception unavailable).
	caAuth atomic.Pointer[tlsca.Authority]
}

// newLocalKeeper builds the in-process keeper over the handle registry, redacting
// egress by default (mode "redact") and installing the default OAuth refresh
// function, which calls the credential's token endpoint and rebuilds the
// Credential with the new access/refresh token and expiry (all other fields
// preserved).
func newLocalKeeper(h *Handles) *localKeeper {
	k := &localKeeper{
		handles:     h,
		refLocks:    map[string]*sync.Mutex{},
		lastForced:  map[string]time.Time{},
		needsReauth: map[string]bool{},
	}
	k.redactor.Store(NewRedactor("redact")) // redact egress by default
	k.refresh = func(ctx context.Context, cred Credential) (Credential, error) {
		tok, err := oauth.Refresh(ctx, http.DefaultClient, cred.TokenEndpoint, cred.RefreshToken, cred.ClientID, cred.ClientSecret)
		if err != nil {
			return Credential{}, err
		}
		// Copy the pre-refresh credential and overwrite ONLY the rotating fields;
		// Mode/Vendor/BaseURL/TokenEndpoint/ClientID/ClientSecret must survive the
		// vault swap (Replace takes a full Credential, not a delta).
		updated := cred
		updated.Secret = tok.AccessToken
		updated.RefreshToken = tok.RefreshToken
		updated.ExpiresAt = tok.ExpiresAt
		return updated, nil
	}
	return k
}

// SetEgressMode configures egress redaction: "redact" (default), "block", "off".
func (k *localKeeper) SetEgressMode(mode string) { k.redactor.Store(NewRedactor(mode)) }

// SetOAuthPersister wires the sink that durably mirrors a connection's rotated
// OAuth refresh token to disk. Nil (the default) disables write-back.
func (k *localKeeper) SetOAuthPersister(p OAuthPersister) { k.persister = p }

// fingerprint returns an opaque, non-secret handle for an injected access token:
// a truncated SHA-256 hex digest. The front holds this (never the token) so the
// reactive-retry path can detect whether a peer already rotated the credential —
// by comparing fingerprints, not the tokens. It is one-way: the token cannot be
// recovered from it.
func fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:32]
}

// Resolve maps a handle to its credID and non-secret PublicCred, dropping the
// plaintext Credential the underlying registry returns so it never reaches the
// front.
func (k *localKeeper) Resolve(handle string) (string, PublicCred, error) {
	credID, cred, err := k.handles.Resolve(handle)
	if err != nil {
		return "", PublicCred{}, err
	}
	return credID, PublicCred{Mode: cred.Mode, Vendor: cred.Vendor, BaseURL: cred.BaseURL}, nil
}

// InjectAuth = refreshIfStale + authMutation. It re-resolves the handle (the front
// holds no Credential), refreshes a stale OAuth token, and returns the header
// mutation that injects the real secret (for the front to Apply) plus the injected
// token's fingerprint.
func (k *localKeeper) InjectAuth(ctx context.Context, handle, credID string) (HeaderMutation, string, error) {
	_, cred, err := k.handles.Resolve(handle)
	if err != nil {
		return HeaderMutation{}, "", err
	}
	cred, err = k.refreshIfStale(ctx, handle, credID, cred)
	if err != nil {
		return HeaderMutation{}, "", err
	}
	return authMutation(cred), fingerprint(cred.Secret), nil
}

// ForceReinject = forceRefresh + authMutation, keyed on the rejected fingerprint.
// It re-resolves the live credential to refresh from (the front holds none),
// force-refreshes (or reuses a peer's already-rotated token — see forceRefresh),
// and returns the header mutation that injects the result (for the front to Apply).
// The new fingerprint is not returned: the reactive retry is at-most-once, so no
// caller needs it.
func (k *localKeeper) ForceReinject(ctx context.Context, handle, credID, rejectedFingerprint string) (HeaderMutation, error) {
	_, cred, err := k.handles.Resolve(handle)
	if err != nil {
		return HeaderMutation{}, err
	}
	updated, err := k.forceRefresh(ctx, handle, credID, cred, rejectedFingerprint)
	if err != nil {
		return HeaderMutation{}, err
	}
	return authMutation(updated), nil
}

// RedactBody scans a textual egress body (handed in as bytes — no live http
// object, so it is boundary-safe) for the handle's managed secret(s) and returns
// the scrubbed body, whether egress must be BLOCKED (block mode + a hit), and the
// hit count. It re-resolves the handle for its sealed secret (custody stays
// keeper-side); a resolve failure or nil redactor leaves the body untouched. The
// front applies the non-secret textual/size gate before calling, and turns a
// blocked result into a 403 (not forwarding) after.
func (k *localKeeper) RedactBody(handle string, body []byte) (scrubbed []byte, blocked bool, hits int) {
	_, cred, err := k.handles.Resolve(handle)
	redactor := k.redactor.Load()
	if err != nil || redactor == nil {
		return body, false, 0
	}
	managed := []string{cred.Secret}
	if _, tok, ok := strings.Cut(cred.Secret, ":"); ok {
		managed = append(managed, tok) // basic: also scrub the token half of user:token
	}
	red, n, block := redactor.Scan(body, managed...)
	if block {
		return body, true, n // front writes 403; original body is not forwarded
	}
	return red, false, n
}

// FlagReauth resolves handle → WriteBackKey and marks the connection as needing
// reauth. Used by the reactive-retry path when even the refreshed token is
// rejected (the grant is dead); the front holds no WriteBackKey, so it hands the
// keeper the handle.
func (k *localKeeper) FlagReauth(handle string) {
	if _, cred, err := k.handles.Resolve(handle); err == nil {
		k.flagReauth(cred.WriteBackKey)
	}
}

// SCRAMProof is the SCRAM password-bearing step; see the Keeper interface doc.
// It re-resolves handle for its credential — same custody rule as
// InjectAuth/RedactBody, the front holds no Credential — then derives the
// proof with l4's shared RFC 7677 arithmetic (l4.ComputeSCRAMProof), so that
// math exists in exactly one place: l4.localSCRAMAuthenticator.Proof (still
// the vector TestSCRAM_RFC7677 exercises in-process) and this keeper-side path
// call the identical code. An L4 datastore credential carries its password in
// the DSN userinfo of BaseURL, not Secret (see connector.Credential), hence
// the l4.TargetFromDSN parse. No per-credID lock is taken: unlike a refresh,
// this never mutates the vault, so it can't race one (see credLock).
func (k *localKeeper) SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error) {
	_, cred, err := k.handles.Resolve(handle)
	if err != nil {
		return nil, err
	}
	target, err := l4.TargetFromDSN(cred.BaseURL)
	if err != nil {
		return nil, err
	}
	return l4.ComputeSCRAMProof(target.Pass, salt, iter, authMessage)
}

// NeedsReauth returns the WriteBackKeys of connections whose most recent OAuth
// refresh attempt failed, sorted for stable output. A key clears once the host
// re-stores a fresh credential for it (Broker.Store).
func (k *localKeeper) NeedsReauth() []string {
	k.reauthMu.Lock()
	defer k.reauthMu.Unlock()
	keys := make([]string, 0, len(k.needsReauth))
	for key, flagged := range k.needsReauth {
		if flagged {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// ClearReauth clears the needs-reauth flag for key, if any is set. Called when
// the host re-stores a credential for key — a successful reauth.
func (k *localKeeper) ClearReauth(key string) {
	k.reauthMu.Lock()
	defer k.reauthMu.Unlock()
	delete(k.needsReauth, key)
}

// refreshIfStale refreshes an OAuth credential nearing expiry and persists the
// new token in the vault (same credID) so the handle keeps resolving. Refreshes
// of one credential are serialized per credID: N racing MCP requests trigger a
// single token request. Returns the credential to inject, or an error (which the
// caller maps to a fail-closed 401). Non-OAuth credentials, and OAuth ones with
// no known expiry or still comfortably valid, are returned untouched.
//
// Two side effects ride along with a refresh, both keyed off cred.WriteBackKey
// (empty means "not a write-back-eligible connection" — neither fires):
//   - On refresh failure, the connection is flagged in needsReauth so operators
//     (and `connect reauth`) see it needs attention.
//   - On success, if the refresh token actually rotated, the new material is
//     mirrored to disk via k.persister (best-effort — a persist failure is
//     logged secret-free and does not fail the request; the in-memory vault
//     already has the rotated credential either way).
func (k *localKeeper) refreshIfStale(ctx context.Context, handle, credID string, cred Credential) (Credential, error) {
	if cred.Mode != ModeOAuthBearer || cred.ExpiresAt.IsZero() || time.Now().Add(refreshSkew).Before(cred.ExpiresAt) {
		return cred, nil
	}
	lk := k.credLock(credID)
	lk.Lock()
	defer lk.Unlock()
	// Re-read under the lock: another request may have just refreshed this
	// credential while we waited, in which case reuse its fresh token.
	if _, fresh, err := k.handles.Resolve(handle); err == nil &&
		!fresh.ExpiresAt.IsZero() && time.Now().Add(refreshSkew).Before(fresh.ExpiresAt) {
		return fresh, nil
	}
	return k.rotate(ctx, handle, cred)
}

// forceRefresh unconditionally refreshes cred's OAuth token — skipping the skew
// check refreshIfStale applies — and swaps the result into the vault under the
// same credID. It shares the per-credID lock with refreshIfStale so a proactive
// and a reactive refresh of one credential can't race, and the same
// rotate-and-persist/flag-on-failure semantics (via rotate). Used by the
// reactive retry path when an upstream 401s a token that wasn't yet stale (an
// early revocation inside the refresh skew).
//
// rejectedFingerprint is the fingerprint of the access token the upstream just
// rejected (captured by the caller BEFORE acquiring the lock, since a peer may
// rotate the credential out from under it while it waits). Under the lock,
// forceRefresh re-reads the LIVE credential and compares fingerprint(live.Secret)
// against rejectedFingerprint — keyed on the fingerprint, not on ExpiresAt the
// way refreshIfStale's re-read is, because a reactive 401 carries no expiry
// signal to compare against (the rejected token wasn't stale by expiry — that's
// WHY this is the reactive path). If the live fingerprint already differs, a peer
// serialized ahead of us on this same lock already rotated the refresh token;
// calling k.refresh again would replay that now-consumed refresh token a second
// time, and a provider with rotation-reuse detection (Google/Okta — OAuth 2.1's
// recommended posture) treats reuse as theft and revokes the WHOLE token family,
// killing the peer's brand-new access token too. So: live != rejected -> hand
// back the peer's live credential, no second rotation. live == rejected (nobody
// has refreshed yet) -> do the real refresh.
func (k *localKeeper) forceRefresh(ctx context.Context, handle, credID string, cred Credential, rejectedFingerprint string) (Credential, error) {
	lk := k.credLock(credID)
	lk.Lock()
	defer lk.Unlock()
	if _, live, err := k.handles.Resolve(handle); err == nil && fingerprint(live.Secret) != rejectedFingerprint {
		return live, nil // a peer already refreshed this credential; reuse it
	}
	// Rate-limit forced rotation per credID (audit I1): a compromised front could
	// otherwise loop ForceReinject to churn the refresh token and hammer the token
	// endpoint. Within the window, reuse the current (already-just-rotated) token
	// rather than rotating again — rotating a fresh token achieves nothing, and a
	// legitimate reactive-retry forces at most once per request.
	if k.forcedRecently(credID) {
		if _, live, err := k.handles.Resolve(handle); err == nil {
			return live, nil
		}
	}
	k.markForced(credID)
	return k.rotate(ctx, handle, cred)
}

// rotate refreshes cred, swaps the fresh credential into the vault under handle,
// mirrors a rotated refresh token via the persister, and flags needs-reauth on
// failure. The caller MUST already hold the per-credID lock (k.credLock(credID)).
// It is the shared core of the proactive (refreshIfStale) and reactive
// (forceRefresh) refresh paths — changing it changes both.
func (k *localKeeper) rotate(ctx context.Context, handle string, cred Credential) (Credential, error) {
	updated, err := k.refresh(ctx, cred)
	if err != nil {
		k.flagReauth(cred.WriteBackKey)
		return Credential{}, err
	}
	if err := k.handles.ReplaceCred(handle, updated); err != nil {
		return Credential{}, err
	}
	k.persistRotation(cred, updated)
	return updated, nil
}

// persistRotation mirrors a rotated OAuth refresh token to disk (best-effort),
// keyed by the connection's WriteBackKey. A no-op when nothing rotated, the
// connection isn't write-back-eligible (empty WriteBackKey), or no persister is
// wired. A persist failure is logged secret-free and never fails the request —
// the in-memory vault already holds the rotated credential.
func (k *localKeeper) persistRotation(old, updated Credential) {
	if updated.RefreshToken == old.RefreshToken || old.WriteBackKey == "" || k.persister == nil {
		return
	}
	m := connMirror{
		AccessToken:   updated.Secret,
		RefreshToken:  updated.RefreshToken,
		TokenEndpoint: updated.TokenEndpoint,
		ClientID:      updated.ClientID,
		ClientSecret:  updated.ClientSecret,
		ExpiresAt:     updated.ExpiresAt,
		RotatedAt:     time.Now(),
	}
	if err := k.persister.Persist(old.WriteBackKey, m); err != nil {
		log.Printf("broker: oauth mirror persist failed for %q: %v", old.WriteBackKey, err)
	}
}

// flagReauth marks a connection as needing `connect reauth` after a failed
// refresh (or a reactive retry that still 401s). A no-op for a connection with
// no WriteBackKey (not write-back-eligible), so an empty key never pollutes
// NeedsReauth().
func (k *localKeeper) flagReauth(key string) {
	if key == "" {
		return
	}
	k.reauthMu.Lock()
	k.needsReauth[key] = true
	k.reauthMu.Unlock()
}

// credLock returns the per-credID mutex that serializes refreshes of one
// credential, creating it on first use.
func (k *localKeeper) credLock(credID string) *sync.Mutex {
	k.refMu.Lock()
	defer k.refMu.Unlock()
	lk := k.refLocks[credID]
	if lk == nil {
		lk = &sync.Mutex{}
		k.refLocks[credID] = lk
	}
	return lk
}

// forcedRecently reports whether a reactive (forced) rotation of credID happened
// within minForcedRefreshInterval — the rate-limit forceRefresh applies against a
// looping compromised front (audit I1).
func (k *localKeeper) forcedRecently(credID string) bool {
	k.refMu.Lock()
	defer k.refMu.Unlock()
	last, ok := k.lastForced[credID]
	return ok && time.Since(last) < minForcedRefreshInterval
}

// markForced records that a forced rotation was attempted for credID now.
func (k *localKeeper) markForced(credID string) {
	k.refMu.Lock()
	k.lastForced[credID] = time.Now()
	k.refMu.Unlock()
}

// authMutation computes the header mutation that injects cred's real secret in
// the header its mode expects, always deleting the incoming handle's auth headers
// first. Pure (no side effects) so it is boundary-safe: the front applies the
// result. It is the value-returning form of the former applyAuth — same headers,
// same values.
func authMutation(cred Credential) HeaderMutation {
	m := HeaderMutation{Delete: []string{"Authorization", "X-Api-Key"}, Set: map[string]string{}}
	switch cred.Mode {
	case ModeAPIKey:
		m.Set["X-Api-Key"] = cred.Secret
	case ModeGoogleAPIKey:
		// gemini-cli's SDK sends the handle in x-goog-api-key too (alongside the
		// Bearer handleFromAuth read); drop it and inject the real key there
		// (Delete-before-Set nets to Set).
		m.Delete = append(m.Delete, "X-Goog-Api-Key")
		m.Set["X-Goog-Api-Key"] = cred.Secret
	case ModeSubscription:
		m.Set["Authorization"] = "Bearer " + cred.Secret
	case ModeOAuthBearer:
		m.Set["Authorization"] = "Bearer " + cred.Secret
	case ModeBasic:
		// Secret is "user:token".
		m.Set["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(cred.Secret))
	case ModeEndpoint:
		if cred.Secret != "" {
			m.Set["Authorization"] = "Bearer " + cred.Secret
		}
	}
	return m
}
