package broker

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The gateway integration tests drive InjectAuth/ForceReinject/RedactBody through
// ServeHTTP, which always resolves the handle BEFORE calling them — so the
// resolve-failure branches of the reshaped (Stage-A value-envelope) keeper methods
// are unreachable that way. These white-box unit tests exercise the value boundary
// directly: the error branches, and the ModeBasic secret split.

// keeperWith builds an in-process keeper holding one credential and returns the
// keeper, a valid handle, and its credID.
func keeperWith(t *testing.T, cred Credential) (*localKeeper, string, string) {
	t.Helper()
	g, handle := gatewayWith(t, cred)
	k := localKeeperOf(g)
	credID, _, err := k.Resolve(handle)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return k, handle, credID
}

func TestKeeper_InjectAuth_ResolveError(t *testing.T) {
	k, _, _ := keeperWith(t, Credential{Mode: ModeSubscription, Secret: "s", BaseURL: "http://x"})
	mut, fp, err := k.InjectAuth(context.Background(), "no-such-handle", "no-such-cred")
	if err == nil {
		t.Fatal("want error for unknown handle, got nil")
	}
	if fp != "" || mut.Delete != nil || mut.Set != nil {
		t.Errorf("want zero mutation + empty fingerprint on error, got mut=%+v fp=%q", mut, fp)
	}
}

func TestKeeper_InjectAuth_RefreshError(t *testing.T) {
	// A stale OAuth credential whose refresh fails -> InjectAuth returns the error
	// (which the front maps to a fail-closed bare 401).
	k, handle, credID := keeperWith(t, Credential{
		Mode: ModeOAuthBearer, Secret: "stale", RefreshToken: "dead",
		ExpiresAt: time.Now().Add(-time.Minute), BaseURL: "http://x", TokenEndpoint: "http://unused",
	})
	k.refresh = func(context.Context, Credential) (Credential, error) {
		return Credential{}, errors.New("refresh boom")
	}
	if _, _, err := k.InjectAuth(context.Background(), handle, credID); err == nil {
		t.Fatal("want error when refresh fails, got nil")
	}
}

func TestKeeper_ForceReinject_ResolveError(t *testing.T) {
	k, _, _ := keeperWith(t, Credential{Mode: ModeOAuthBearer, Secret: "s", BaseURL: "http://x"})
	if _, err := k.ForceReinject(context.Background(), "no-such-handle", "cid", "fp"); err == nil {
		t.Fatal("want error for unknown handle, got nil")
	}
}

func TestKeeper_ForceReinject_RefreshError(t *testing.T) {
	k, handle, credID := keeperWith(t, Credential{
		Mode: ModeOAuthBearer, Secret: "cur", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "http://x", TokenEndpoint: "http://unused",
	})
	k.refresh = func(context.Context, Credential) (Credential, error) {
		return Credential{}, errors.New("refresh boom")
	}
	// rejectedFingerprint == the live token's fingerprint, so forceRefresh rotates
	// (no peer got ahead) and hits the failing refresh.
	rejected := fingerprint("cur")
	if _, err := k.ForceReinject(context.Background(), handle, credID, rejected); err == nil {
		t.Fatal("want error when force refresh fails, got nil")
	}
}

// TestKeeper_ForceRefresh_RateLimited is the regression guard for audit I1: a
// compromised front looping ForceReinject with the current fingerprint each time
// must NOT be able to churn the refresh token — only one forced rotation happens
// per credID within minForcedRefreshInterval; the rest reuse the just-rotated token.
func TestKeeper_ForceRefresh_RateLimited(t *testing.T) {
	k, handle, credID := keeperWith(t, Credential{
		Mode: ModeOAuthBearer, Secret: "tok", RefreshToken: "r0",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://x", TokenEndpoint: "http://unused",
	})
	rotations := 0
	k.refresh = func(_ context.Context, c Credential) (Credential, error) {
		rotations++
		c.Secret += "+" // distinct after each rotation, so the fingerprint changes
		c.ExpiresAt = time.Now().Add(time.Hour)
		return c, nil
	}
	// The attacker re-reads the current fingerprint before each forced call (as it
	// would via InjectAuth) and hammers ForceReinject.
	for i := 0; i < 5; i++ {
		_, cur, err := k.handles.Resolve(handle)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if _, err := k.ForceReinject(context.Background(), handle, credID, fingerprint(cur.Secret)); err != nil {
			t.Fatalf("ForceReinject: %v", err)
		}
	}
	if rotations != 1 {
		t.Errorf("forced-refresh rate limit: %d rotations in a tight loop, want 1", rotations)
	}
}

func TestKeeper_ForceReinject_Success(t *testing.T) {
	k, handle, credID := keeperWith(t, Credential{
		Mode: ModeOAuthBearer, Secret: "cur", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "http://x", TokenEndpoint: "http://unused",
	})
	k.refresh = func(_ context.Context, c Credential) (Credential, error) {
		c.Secret = "rotated"
		c.RefreshToken = "r2"
		c.ExpiresAt = time.Now().Add(time.Hour)
		return c, nil
	}
	mut, err := k.ForceReinject(context.Background(), handle, credID, fingerprint("cur"))
	if err != nil {
		t.Fatalf("ForceReinject: %v", err)
	}
	h := http.Header{}
	mut.Apply(h)
	if got := h.Get("Authorization"); got != "Bearer rotated" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer rotated")
	}
}

func TestKeeper_RedactBody_ResolveErrorLeavesBodyUntouched(t *testing.T) {
	k, _, _ := keeperWith(t, Credential{Mode: ModeAPIKey, Secret: "s", BaseURL: "http://x"})
	body := []byte(`{"hello":"world"}`)
	scrubbed, blocked, hits := k.RedactBody("no-such-handle", body)
	if blocked || hits != 0 || string(scrubbed) != string(body) {
		t.Errorf("resolve-fail: want (body, false, 0), got (%q, %v, %d)", scrubbed, blocked, hits)
	}
}

func TestKeeper_RedactBody_BasicScrubsTokenHalf(t *testing.T) {
	// A ModeBasic secret is "user:token"; RedactBody must also scrub the token half
	// (the strings.Cut branch), so a body echoing the token is redacted.
	const token = "ghp_supersecrettoken0000"
	k, handle, _ := keeperWith(t, Credential{Mode: ModeBasic, Secret: "octocat:" + token, BaseURL: "http://x"})
	body := []byte(`{"leak":"` + token + `"}`)
	scrubbed, blocked, hits := k.RedactBody(handle, body)
	if blocked {
		t.Fatal("default redact mode must not block")
	}
	if hits == 0 || strings.Contains(string(scrubbed), token) {
		t.Errorf("token half not scrubbed: hits=%d scrubbed=%q", hits, scrubbed)
	}
}
