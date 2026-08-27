package broker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/datadir-lab/poddle/src/internal/l4"
	"github.com/datadir-lab/poddle/src/internal/oauth"
)

// refreshSkew is how far before an OAuth access token's ExpiresAt the keeper
// proactively refreshes it, so an in-flight request isn't injected with a token
// that expires mid-flight.
const refreshSkew = 60 * time.Second

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
	// then injects the REAL secret into h per the credential's mode — deleting the
	// pod handle header first. It returns an opaque, non-secret fingerprint of the
	// injected access token; the front holds that, never the token. A non-nil err
	// (refresh failure) maps to the front's fail-closed bare 401.
	InjectAuth(ctx context.Context, handle, credID string, h http.Header) (fingerprint string, err error)

	// ForceReinject is the reactive-retry path (an upstream 401 on a token that
	// wasn't yet stale). Under the per-credID lock it re-reads the live credential
	// and, if a peer already rotated it (the live secret's fingerprint no longer
	// matches the rejected one), reuses that peer's token WITHOUT a second rotation
	// — preserving the refresh-token-family revocation collapse. Otherwise it
	// force-refreshes. Either way it injects the resulting secret into h and returns
	// the new fingerprint. A non-nil err maps to the front's fail-closed bare 401
	// (reauth already flagged keeper-side).
	ForceReinject(ctx context.Context, handle, credID, fingerprint string, h http.Header) (newFingerprint string, err error)

	// RedactBody scrubs managed secrets (and high-confidence patterns) from a
	// textual egress body, rewriting r in place. It lives keeper-side because it
	// needs the sealed managed secret to scan. It returns proceed=false (after
	// writing a 403) when redaction is set to block and a secret is found;
	// otherwise proceed=true and the number of secrets redacted.
	//
	// Call order is load-bearing: the caller must run InjectAuth (which calls
	// refreshIfStale) for handle BEFORE RedactBody, so a just-rotated credential
	// is already persisted to the vault when RedactBody re-resolves handle — the
	// scan then targets the SAME secret that's about to hit the wire, not a
	// stale pre-rotation one.
	RedactBody(w http.ResponseWriter, r *http.Request, handle string) (proceed bool, hits int)

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

// localKeeper satisfies Keeper in-process. It owns the handle registry (the vault
// access path), the OAuth refresh hook and its per-credID serialization, the
// egress redactor, the write-back persister, and the needs-reauth set — i.e. all
// the credential-custody and secret-touching state that used to sit on Gateway.
// Every refresh/rotate/redact/reauth behavior below is moved verbatim from the
// former Gateway; the fingerprint hashing is the only logic new to the refactor.
type localKeeper struct {
	handles  *Handles
	redactor *Redactor

	// refresh mints a fresh Credential from a stale ModeOAuthBearer one (default
	// calls oauth.Refresh; tests override it). refMu guards refLocks, a per-credID
	// mutex map that serializes concurrent refreshes of one credential so N racing
	// requests trigger exactly one token request.
	refresh  func(context.Context, Credential) (Credential, error)
	refMu    sync.Mutex
	refLocks map[string]*sync.Mutex

	// persister durably mirrors a connection's rotated OAuth refresh token to disk
	// (nil = no write-back). reauthMu guards needsReauth, the set of WriteBackKeys
	// whose most recent refresh attempt failed — surfaced via NeedsReauth() and
	// cleared once the host re-stores a fresh credential (Broker.Store).
	persister   OAuthPersister
	reauthMu    sync.Mutex
	needsReauth map[string]bool
}

// newLocalKeeper builds the in-process keeper over the handle registry, redacting
// egress by default (mode "redact") and installing the default OAuth refresh
// function, which calls the credential's token endpoint and rebuilds the
// Credential with the new access/refresh token and expiry (all other fields
// preserved).
func newLocalKeeper(h *Handles) *localKeeper {
	k := &localKeeper{
		handles:     h,
		redactor:    NewRedactor("redact"),
		refLocks:    map[string]*sync.Mutex{},
		needsReauth: map[string]bool{},
	}
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
func (k *localKeeper) SetEgressMode(mode string) { k.redactor = NewRedactor(mode) }

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

// InjectAuth = refreshIfStale + applyAuth. It re-resolves the handle (the front
// holds no Credential), refreshes a stale OAuth token, injects the real secret
// into h, and returns the injected token's fingerprint.
func (k *localKeeper) InjectAuth(ctx context.Context, handle, credID string, h http.Header) (string, error) {
	_, cred, err := k.handles.Resolve(handle)
	if err != nil {
		return "", err
	}
	cred, err = k.refreshIfStale(ctx, handle, credID, cred)
	if err != nil {
		return "", err
	}
	applyAuth(h, cred)
	return fingerprint(cred.Secret), nil
}

// ForceReinject = forceRefresh + applyAuth, keyed on the rejected fingerprint.
// It re-resolves the live credential to refresh from (the front holds none),
// force-refreshes (or reuses a peer's already-rotated token — see forceRefresh),
// injects the result into h, and returns the new fingerprint.
func (k *localKeeper) ForceReinject(ctx context.Context, handle, credID, rejectedFingerprint string, h http.Header) (string, error) {
	_, cred, err := k.handles.Resolve(handle)
	if err != nil {
		return "", err
	}
	updated, err := k.forceRefresh(ctx, handle, credID, cred, rejectedFingerprint)
	if err != nil {
		return "", err
	}
	applyAuth(h, updated)
	return fingerprint(updated.Secret), nil
}

// RedactBody re-resolves the handle for its managed secret and scrubs the egress
// body. A resolve failure here (the handle vanished mid-request — not reachable
// in practice, since InjectAuth just resolved it) leaves the body untouched.
func (k *localKeeper) RedactBody(w http.ResponseWriter, r *http.Request, handle string) (bool, int) {
	_, cred, err := k.handles.Resolve(handle)
	if err != nil {
		return true, 0
	}
	return k.redactBody(w, r, cred)
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

// redactBody scrubs secrets from a textual outbound body, rewriting r in place.
// It returns proceed=false (after writing a 403) when redaction is set to block
// and a secret is found; otherwise proceed=true and the number of secrets
// redacted (0 when nothing was scanned or found).
func (k *localKeeper) redactBody(w http.ResponseWriter, r *http.Request, cred Credential) (proceed bool, hits int) {
	if k.redactor == nil || r.Body == nil || !isTextual(r.Header.Get("Content-Type")) || r.ContentLength > maxScanBytes {
		return true, 0
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxScanBytes))
	_ = r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return true, 0
	}
	managed := []string{cred.Secret}
	if _, tok, ok := strings.Cut(cred.Secret, ":"); ok {
		managed = append(managed, tok) // basic: also scrub the token half of user:token
	}
	red, n, block := k.redactor.Scan(body, managed...)
	if block {
		http.Error(w, "poddle: outbound request blocked — secret detected", http.StatusForbidden)
		return false, n
	}
	r.Body = io.NopCloser(bytes.NewReader(red))
	r.ContentLength = int64(len(red))
	r.Header.Set("Content-Length", strconv.Itoa(len(red)))
	return true, n
}

// applyAuth clears the incoming handle and injects the real secret in the header
// the credential's mode expects. It runs keeper-side — the plaintext secret lives
// in the request header only after this call, inside the vault-owned code path.
func applyAuth(h http.Header, cred Credential) {
	h.Del("Authorization")
	h.Del("X-Api-Key")
	switch cred.Mode {
	case ModeAPIKey:
		h.Set("X-Api-Key", cred.Secret)
	case ModeGoogleAPIKey:
		// gemini-cli's SDK sends the handle in x-goog-api-key too (alongside the
		// Bearer handleFromAuth read); drop it and inject the real key there.
		h.Del("X-Goog-Api-Key")
		h.Set("X-Goog-Api-Key", cred.Secret)
	case ModeSubscription:
		h.Set("Authorization", "Bearer "+cred.Secret)
	case ModeOAuthBearer:
		h.Set("Authorization", "Bearer "+cred.Secret)
	case ModeBasic:
		// Secret is "user:token".
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cred.Secret)))
	case ModeEndpoint:
		if cred.Secret != "" {
			h.Set("Authorization", "Bearer "+cred.Secret)
		}
	}
}
