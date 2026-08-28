package broker

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// rpcPair wires a socketKeeperClient to a serveKeeper loop over an in-process
// net.Pipe, both serving the real *localKeeper behind the given credential. It
// proves the full Stage-A Keeper interface round-trips over a stream conn (the
// wire contract Phase 2's socketpair carries) without a subprocess — the process
// split itself is proven separately by internal/privsep.
func rpcPair(t *testing.T, cred Credential) (*socketKeeperClient, *localKeeper, string, string) {
	t.Helper()
	k, handle, credID := keeperWith(t, cred)
	cliConn, srvConn := net.Pipe()
	go func() { _ = serveKeeper(srvConn, k) }()
	c := newSocketKeeperClient(cliConn)
	t.Cleanup(func() { _ = c.Close(); _ = srvConn.Close() })
	return c, k, handle, credID
}

func TestKeeperRPC_Resolve(t *testing.T) {
	c, k, handle, _ := rpcPair(t, Credential{Mode: ModeSubscription, Secret: "s", Vendor: "anthropic", BaseURL: "https://api.example"})
	wantID, wantPub, err := k.Resolve(handle)
	if err != nil {
		t.Fatalf("direct resolve: %v", err)
	}
	gotID, gotPub, err := c.Resolve(handle)
	if err != nil {
		t.Fatalf("rpc resolve: %v", err)
	}
	if gotID != wantID || gotPub != wantPub {
		t.Errorf("resolve mismatch: rpc=(%q,%+v) direct=(%q,%+v)", gotID, gotPub, wantID, wantPub)
	}
}

func TestKeeperRPC_Resolve_ErrorPropagates(t *testing.T) {
	c, _, _, _ := rpcPair(t, Credential{Mode: ModeAPIKey, Secret: "s", BaseURL: "https://x"})
	if _, _, err := c.Resolve("no-such-handle"); err == nil {
		t.Fatal("want error for unknown handle over RPC, got nil")
	}
}

func TestKeeperRPC_InjectAuth_CarriesHeaderMutation(t *testing.T) {
	const secret = "real-access-token"
	c, _, handle, credID := rpcPair(t, Credential{Mode: ModeSubscription, Secret: secret, BaseURL: "https://x"})
	mut, fp, err := c.InjectAuth(context.Background(), handle, credID)
	if err != nil {
		t.Fatalf("rpc InjectAuth: %v", err)
	}
	h := http.Header{"Authorization": []string{"Bearer " + handle}}
	mut.Apply(h)
	if got := h.Get("Authorization"); got != "Bearer "+secret {
		t.Errorf("mutation did not inject the real secret across RPC: Authorization=%q", got)
	}
	if fp != fingerprint(secret) {
		t.Errorf("fingerprint mismatch across RPC: got %q want %q", fp, fingerprint(secret))
	}
}

func TestKeeperRPC_InjectAuth_AllModes(t *testing.T) {
	// The HeaderMutation must survive gob for every mode's Delete/Set shape.
	cases := []struct {
		mode   Mode
		secret string
		check  func(t *testing.T, h http.Header)
	}{
		{ModeAPIKey, "k", func(t *testing.T, h http.Header) {
			if h.Get("X-Api-Key") != "k" || h.Get("Authorization") != "" {
				t.Errorf("apikey: X-Api-Key=%q Authorization=%q", h.Get("X-Api-Key"), h.Get("Authorization"))
			}
		}},
		{ModeGoogleAPIKey, "g", func(t *testing.T, h http.Header) {
			if h.Get("X-Goog-Api-Key") != "g" {
				t.Errorf("google: X-Goog-Api-Key=%q", h.Get("X-Goog-Api-Key"))
			}
		}},
		{ModeBasic, "user:tok", func(t *testing.T, h http.Header) {
			if got := h.Get("Authorization"); got != "Basic dXNlcjp0b2s=" {
				t.Errorf("basic: Authorization=%q", got)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			c, _, handle, credID := rpcPair(t, Credential{Mode: tc.mode, Secret: tc.secret, BaseURL: "https://x"})
			mut, _, err := c.InjectAuth(context.Background(), handle, credID)
			if err != nil {
				t.Fatalf("InjectAuth: %v", err)
			}
			h := http.Header{
				"Authorization":  []string{"Bearer " + handle},
				"X-Api-Key":      []string{handle},
				"X-Goog-Api-Key": []string{handle},
			}
			mut.Apply(h)
			tc.check(t, h)
		})
	}
}

func TestKeeperRPC_ForceReinject(t *testing.T) {
	c, k, handle, credID := rpcPair(t, Credential{
		Mode: ModeOAuthBearer, Secret: "cur", RefreshToken: "r1",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: "https://x", TokenEndpoint: "http://unused",
	})
	k.refresh = func(_ context.Context, cr Credential) (Credential, error) {
		cr.Secret = "rotated"
		cr.RefreshToken = "r2"
		cr.ExpiresAt = time.Now().Add(time.Hour)
		return cr, nil
	}
	mut, err := c.ForceReinject(context.Background(), handle, credID, fingerprint("cur"))
	if err != nil {
		t.Fatalf("rpc ForceReinject: %v", err)
	}
	h := http.Header{}
	mut.Apply(h)
	if got := h.Get("Authorization"); got != "Bearer rotated" {
		t.Errorf("ForceReinject across RPC: Authorization=%q want %q", got, "Bearer rotated")
	}
}

func TestKeeperRPC_RedactBody(t *testing.T) {
	const token = "ghp_supersecrettoken0000"
	c, k, handle, _ := rpcPair(t, Credential{Mode: ModeBasic, Secret: "octocat:" + token, BaseURL: "https://x"})
	body := []byte(`{"leak":"` + token + `"}`)
	scrubbed, blocked, hits := c.RedactBody(handle, body)
	if blocked {
		t.Fatal("default redact mode must not block")
	}
	if hits == 0 || bytes.Contains(scrubbed, []byte(token)) {
		t.Errorf("token not scrubbed across RPC: hits=%d scrubbed=%q", hits, scrubbed)
	}
	// Byte-identical to the direct call.
	wantScrub, wantBlock, wantHits := k.RedactBody(handle, body)
	if !bytes.Equal(scrubbed, wantScrub) || blocked != wantBlock || hits != wantHits {
		t.Errorf("rpc redact != direct: (%q,%v,%d) vs (%q,%v,%d)", scrubbed, blocked, hits, wantScrub, wantBlock, wantHits)
	}
}

func TestKeeperRPC_SCRAMProof(t *testing.T) {
	c, k, handle, _ := rpcPair(t, Credential{Mode: ModeEndpoint, BaseURL: "postgres://scott:tiger@localhost:5432/db"})
	salt := []byte("saltsaltsalt")
	const iter = 4096
	authMsg := "n=user,r=clientnonce,r=servernonce,s=c2FsdA==,i=4096,c=biws,r=servernonce"
	want, err := k.SCRAMProof(handle, salt, iter, authMsg)
	if err != nil {
		t.Fatalf("direct SCRAMProof: %v", err)
	}
	got, err := c.SCRAMProof(handle, salt, iter, authMsg)
	if err != nil {
		t.Fatalf("rpc SCRAMProof: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("SCRAM proof mismatch across RPC:\n got=%x\nwant=%x", got, want)
	}
}

func TestKeeperRPC_ReauthLifecycle(t *testing.T) {
	c, _, handle, _ := rpcPair(t, Credential{Mode: ModeOAuthBearer, Secret: "s", BaseURL: "https://x", WriteBackKey: "gh"})
	if got := c.NeedsReauth(); len(got) != 0 {
		t.Fatalf("NeedsReauth should start empty, got %v", got)
	}
	c.FlagReauth(handle)
	// FlagReauth is fire-and-forget over RPC; poll briefly for it to land.
	deadline := time.Now().Add(2 * time.Second)
	var keys []string
	for time.Now().Before(deadline) {
		if keys = c.NeedsReauth(); len(keys) == 1 && keys[0] == "gh" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(keys) != 1 || keys[0] != "gh" {
		t.Fatalf("after FlagReauth, NeedsReauth = %v, want [gh]", keys)
	}
	c.ClearReauth("gh")
	for time.Now().Before(deadline) {
		if len(c.NeedsReauth()) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("after ClearReauth, NeedsReauth = %v, want empty", c.NeedsReauth())
}

func TestKeeperRPC_SetEgressMode(t *testing.T) {
	const secret = "sk-secretsecret0000"
	c, _, handle, _ := rpcPair(t, Credential{Mode: ModeAPIKey, Secret: secret, BaseURL: "https://x"})
	body := []byte(`{"k":"` + secret + `"}`)
	// Default redact mode scrubs.
	if _, _, hits := c.RedactBody(handle, body); hits == 0 {
		t.Fatal("expected a redaction hit under default mode")
	}
	c.SetEgressMode("off")
	// "off" must be observed keeper-side on the next scan (fire-and-forget; poll).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, hits := c.RedactBody(handle, body); hits == 0 {
			return // egress off observed
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("SetEgressMode(off) not observed: RedactBody still reporting hits")
}

// panicKeeper wraps a real Keeper but panics on Resolve, to prove serveKeeper
// contains a per-request panic instead of crashing the whole keeper.
type panicKeeper struct{ Keeper }

func (panicKeeper) Resolve(string) (string, PublicCred, error) { panic("boom in Resolve") }

func TestKeeperRPC_PanicIsContained(t *testing.T) {
	k, handle, _ := keeperWith(t, Credential{Mode: ModeEndpoint, BaseURL: "postgres://scott:tiger@localhost:5432/db"})
	cliConn, srvConn := net.Pipe()
	go func() { _ = serveKeeper(srvConn, panicKeeper{Keeper: k}) }()
	c := newSocketKeeperClient(cliConn)
	t.Cleanup(func() { _ = c.Close(); _ = srvConn.Close() })

	// The panicking method returns a fail-closed error to just this caller...
	if _, _, err := c.Resolve(handle); err == nil {
		t.Fatal("want error from a panicking keeper method, got nil")
	}
	// ...and the keeper SURVIVED: a subsequent request on a non-panicking method
	// (SCRAMProof delegates to the embedded keeper, bypassing panicKeeper.Resolve)
	// still gets served. A crashed keeper would fail this RPC instead.
	if _, err := c.SCRAMProof(handle, []byte("saltsalt"), 4096, "n=x,r=y"); err != nil {
		t.Fatalf("keeper did not survive the contained panic: SCRAMProof failed: %v", err)
	}
}

func TestKeeperRPC_BackpressureNoDeadlock(t *testing.T) {
	// Lower the in-flight cap so more requests contend than there are slots, exercising
	// the semaphore backpressure path: every request must still complete (no deadlock)
	// and correlate correctly.
	saved := keeperInflightLimit
	keeperInflightLimit = 4
	t.Cleanup(func() { keeperInflightLimit = saved })

	c, _, handle, wantID := rpcPair(t, Credential{Mode: ModeSubscription, Secret: "s", BaseURL: "https://x"})
	const n = 40
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _, err := c.Resolve(handle)
			if err != nil {
				errs <- err
			} else if id != wantID {
				errs <- errWrongID(id, wantID)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("backpressure call: %v", err)
	}
}

func TestKeeperRPC_SCRAMIterCapped(t *testing.T) {
	c, _, handle, _ := rpcPair(t, Credential{Mode: ModeEndpoint, BaseURL: "postgres://scott:tiger@localhost:5432/db"})
	if _, err := c.SCRAMProof(handle, []byte("saltsalt"), maxSCRAMIter+1, "n=x,r=y"); err == nil {
		t.Fatal("want error for a SCRAM iteration count over the cap, got nil")
	}
	// A sane count still works.
	if _, err := c.SCRAMProof(handle, []byte("saltsalt"), 4096, "n=x,r=y"); err != nil {
		t.Fatalf("legit iter rejected: %v", err)
	}
}

func TestKeeperRPC_ConcurrentCallsMultiplex(t *testing.T) {
	c, _, handle, wantID := rpcPair(t, Credential{Mode: ModeSubscription, Secret: "s", BaseURL: "https://x"})
	credID, _, _ := c.Resolve(handle)
	if credID != wantID {
		t.Fatalf("resolve credID=%q want %q", credID, wantID)
	}
	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _, err := c.Resolve(handle)
			if err != nil {
				errs <- err
				return
			}
			if id != wantID {
				errs <- errWrongID(id, wantID)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call: %v", err)
	}
}

// FuzzKeeperServer feeds arbitrary bytes down the keeper's untrusted-request path
// (envelope decode -> dispatch -> per-method payload decode) and asserts it never
// panics — the front is untrusted under the privsep threat model, so a malformed or
// hostile frame must fail cleanly (error, not crash), keeping the keeper fail-closed
// rather than exploitable. Joins the redactor/proxy-auth fuzzers.
func FuzzKeeperServer(f *testing.F) {
	v := NewVault()
	h := NewHandles(v)
	id, err := v.Store("local", Credential{Mode: ModeSubscription, Secret: "s", BaseURL: "https://x"})
	if err != nil {
		f.Fatalf("store: %v", err)
	}
	handle, err := h.IssueHandle("local", id, "box", time.Hour)
	if err != nil {
		f.Fatalf("issue: %v", err)
	}
	k := newLocalKeeper(h)

	f.Add([]byte(nil))
	f.Add([]byte("garbage"))
	f.Add(bytes.Repeat([]byte{0xff}, 64))
	for _, m := range []string{mResolve, mInjectAuth, mForceReinject, mRedactBody, mSCRAMProof, mNeedsReauth, mClearReauth, mFlagReauth, mSetEgressMode, "bogus"} {
		payload, _ := gobEncode(rpcRequest{ID: 1, Method: m})
		f.Add(payload)
	}
	valid, _ := gobEncode(rpcRequest{ID: 7, Method: mResolve, Body: mustGob(f, resolveReq{Handle: handle.Value})})
	f.Add(valid)

	f.Fuzz(func(t *testing.T, frame []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("keeper decode/dispatch panicked on untrusted frame: %v", r)
			}
		}()
		var req rpcRequest
		if derr := gobDecode(frame, &req); derr != nil {
			return // malformed envelope rejected cleanly — expected
		}
		_, _ = dispatchKeeper(k, req) // must return (possibly an error), never panic
	})
}

func mustGob(tb testing.TB, v any) []byte {
	tb.Helper()
	b, err := gobEncode(v)
	if err != nil {
		tb.Fatalf("gobEncode: %v", err)
	}
	return b
}

func TestKeeperRPC_DeadKeeperFailsClosed(t *testing.T) {
	k, handle, credID := keeperWith(t, Credential{Mode: ModeSubscription, Secret: "s", BaseURL: "https://x"})
	cliConn, srvConn := net.Pipe()
	done := make(chan struct{})
	go func() { _ = serveKeeper(srvConn, k); close(done) }()
	c := newSocketKeeperClient(cliConn)
	t.Cleanup(func() { _ = c.Close() })

	// Kill the keeper: closing the server end makes the client's writes and its
	// readLoop fail, so every subsequent call must fail (not hang).
	_ = srvConn.Close()
	<-done

	// Request-path methods return an error (the gateway maps these to 401/block).
	if _, _, err := c.Resolve(handle); err == nil {
		t.Error("Resolve should error on a dead keeper")
	}
	if _, _, err := c.InjectAuth(context.Background(), handle, credID); err == nil {
		t.Error("InjectAuth should error on a dead keeper")
	}
	if _, err := c.ForceReinject(context.Background(), handle, credID, "fp"); err == nil {
		t.Error("ForceReinject should error on a dead keeper")
	}
	if _, err := c.SCRAMProof(handle, []byte("salt"), 4096, "m"); err == nil {
		t.Error("SCRAMProof should error on a dead keeper")
	}
	// RedactBody has no error return — it fails CLOSED by blocking (the gateway turns
	// blocked into a 403), never forwarding an unscanned body.
	if _, blocked, _ := c.RedactBody(handle, []byte("body")); !blocked {
		t.Error("RedactBody should fail closed (block) on a dead keeper")
	}
	// Management methods are best-effort: no hang, no panic; NeedsReauth returns nil.
	if got := c.NeedsReauth(); got != nil {
		t.Errorf("NeedsReauth on a dead keeper = %v, want nil", got)
	}
	c.ClearReauth("key")
	c.FlagReauth(handle)
	c.SetEgressMode("off")
}

func TestKeeperRPC_CallAfterCloseErrors(t *testing.T) {
	c, _, handle, _ := rpcPair(t, Credential{Mode: ModeSubscription, Secret: "s", BaseURL: "https://x"})
	if _, _, err := c.Resolve(handle); err != nil {
		t.Fatalf("resolve before close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, _, err := c.Resolve(handle); err == nil {
		t.Error("Resolve after Close should error")
	}
}

func errWrongID(got, want string) error {
	return &wrongIDError{got: got, want: want}
}

type wrongIDError struct{ got, want string }

func (e *wrongIDError) Error() string {
	return "response correlated to wrong request: credID=" + e.got + " want " + e.want
}
