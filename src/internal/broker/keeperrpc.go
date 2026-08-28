package broker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// This file carries the Stage-A value-shaped Keeper interface over a stream conn,
// so Phase 2 can run the keeper in a separate process (the vault + secret custody)
// from the gateway FRONT (untrusted request parsing) — a front RCE then cannot read
// the vault's address space. It is platform-agnostic (no socketpair/fd-passing
// here — that is privsep's Linux-only job, which supplies the *net.UnixConn at
// runtime); this is the wire contract, identical regardless of which process forks
// which. See docs/design/broker-privilege-separation.md.

// maxKeeperFrameLen bounds an untrusted length prefix so a hostile or corrupt peer
// can't drive an unbounded allocation. Keeper payloads are small (a header set, a
// SCRAM proof, a scrubbed JSON body <= maxScanBytes); 32 MiB is generous headroom
// over the 25 MiB egress-scan ceiling a RedactBody body can carry.
const maxKeeperFrameLen = 32 << 20

// defaultKeeperCallTimeout bounds a keeper call that carries no caller context
// (Resolve/RedactBody/SCRAMProof and the management methods), so a wedged keeper
// can't hang the front forever. InjectAuth/ForceReinject honor the caller's ctx.
const defaultKeeperCallTimeout = 30 * time.Second

// keeperInflightLimit bounds concurrent request dispatch on the keeper so a hostile
// front flooding requests can't spawn unbounded goroutines (each pinning up to a
// maxKeeperFrameLen body). The read loop blocks on the semaphore past this many
// in-flight requests, applying natural socket backpressure to the front. It is a var
// only so a test can lower it to exercise the backpressure path cheaply.
var keeperInflightLimit = 256

const (
	// keeperOpTimeout bounds a keeper-side op that would otherwise inherit the
	// caller's context (which can't cross the wire) — notably the OAuth refresh in
	// InjectAuth/ForceReinject — so a slow or hostile token endpoint can't wedge a
	// keeper goroutine forever (which would leak a goroutine + a connection).
	keeperOpTimeout = 30 * time.Second

	// maxSCRAMIter caps the attacker-controllable PBKDF2 iteration count a front can
	// pass over the wire, so a compromised front can't pin a keeper core with a huge
	// count. Legitimate Postgres SCRAM iteration counts are far below this (the
	// server default is 4096).
	maxSCRAMIter = 1 << 20
)

// rpcRequest is a framed keeper request. ID correlates the response; Method selects
// the handler; Body is the gob-encoded per-method argument struct.
type rpcRequest struct {
	ID     uint64
	Method string
	Body   []byte
}

// rpcResponse is a framed keeper response. ID echoes the request; Err carries a
// stringified keeper-side error (gob can't transport a Go error), empty on success;
// Body is the gob-encoded per-method result struct.
type rpcResponse struct {
	ID   uint64
	Body []byte
	Err  string
}

// Method tags. Kept as short strings for legible wire dumps and forward-compatible
// dispatch (an unknown method is a clean error, not a panic).
const (
	mResolve       = "resolve"
	mInjectAuth    = "inject"
	mForceReinject = "forcereinject"
	mRedactBody    = "redact"
	mSCRAMProof    = "scram"
	mNeedsReauth   = "needsreauth"
	mClearReauth   = "clearreauth"
	mFlagReauth    = "flagreauth"
	mSetEgressMode = "setegress"
)

// --- per-method payloads (gob-encoded into rpcRequest/rpcResponse Body) ---

type resolveReq struct{ Handle string }
type resolveResp struct {
	CredID string
	Pub    PublicCred
}

type injectReq struct{ Handle, CredID string }
type injectResp struct {
	Mut         HeaderMutation
	Fingerprint string
}

type forceReinjectReq struct{ Handle, CredID, RejectedFingerprint string }
type forceReinjectResp struct{ Mut HeaderMutation }

type redactReq struct {
	Handle string
	Body   []byte
}
type redactResp struct {
	Scrubbed []byte
	Blocked  bool
	Hits     int
}

type scramReq struct {
	Handle      string
	Salt        []byte
	Iter        int
	AuthMessage string
}
type scramResp struct{ Proof []byte }

type needsReauthResp struct{ Keys []string }
type clearReauthReq struct{ Key string }
type flagReauthReq struct{ Handle string }
type setEgressReq struct{ Mode string }

// ============================ client (FRONT side) ============================

// socketKeeperClient is the FRONT-side stub: it implements broker.Keeper by
// framing each call to the keeper process over a stream conn and awaiting the
// tagged response. Concurrent callers are multiplexed over the single conn — a
// background reader demuxes responses by ID — so a blocking keeper op (e.g. an
// OAuth refresh inside InjectAuth) never head-of-line-blocks other requests.
type socketKeeperClient struct {
	conn net.Conn

	wmu sync.Mutex // serializes frame writes onto conn

	mu        sync.Mutex // guards nextID, pending, closedErr
	nextID    uint64
	pending   map[uint64]chan rpcResponse
	closedErr error
}

var _ Keeper = (*socketKeeperClient)(nil)

// newSocketKeeperClient wraps conn and starts the response-demux reader. The
// caller owns conn's lifetime; Close stops the reader and fails in-flight calls.
func newSocketKeeperClient(conn net.Conn) *socketKeeperClient {
	c := &socketKeeperClient{conn: conn, pending: map[uint64]chan rpcResponse{}}
	go c.readLoop()
	return c
}

// readLoop reads response frames and routes each to its waiting caller by ID until
// the conn errors or closes, at which point it fails all pending calls (and any
// future call) with that error — no caller ever hangs on a dead keeper.
func (c *socketKeeperClient) readLoop() {
	for {
		frame, err := readFrameConn(c.conn)
		if err != nil {
			c.fail(fmt.Errorf("keeper unreachable: %w", err))
			return
		}
		var resp rpcResponse
		if derr := gobDecode(frame, &resp); derr != nil {
			c.fail(fmt.Errorf("keeper response decode: %w", derr))
			return
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp // buffered(1); never blocks
		}
	}
}

// fail marks the client permanently closed with err and drains every pending
// waiter so no call blocks forever. Idempotent.
func (c *socketKeeperClient) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closedErr == nil {
		c.closedErr = err
	}
	for id, ch := range c.pending {
		ch <- rpcResponse{ID: id, Err: c.closedErr.Error()}
		delete(c.pending, id)
	}
}

// Close fails in-flight calls and closes the underlying conn (which also unblocks
// the reader). Safe to call once; the conn close is what stops readLoop.
func (c *socketKeeperClient) Close() error {
	c.fail(errors.New("keeper client closed"))
	return c.conn.Close()
}

// call frames a request, waits for the correlated response (or ctx cancellation),
// and returns the raw response Body or a keeper-side error.
func (c *socketKeeperClient) call(ctx context.Context, method string, reqPayload any) ([]byte, error) {
	// A nil payload sends an empty Body (methods with no args, e.g. NeedsReauth) —
	// gob refuses to encode an argless struct, so we skip encoding entirely.
	var body []byte
	if reqPayload != nil {
		b, err := gobEncode(reqPayload)
		if err != nil {
			return nil, fmt.Errorf("keeper request encode: %w", err)
		}
		body = b
	}

	c.mu.Lock()
	if c.closedErr != nil {
		c.mu.Unlock()
		return nil, c.closedErr
	}
	id := c.nextID
	c.nextID++
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	frame, err := gobEncode(rpcRequest{ID: id, Method: method, Body: body})
	if err != nil {
		c.forget(id)
		return nil, fmt.Errorf("keeper frame encode: %w", err)
	}
	c.wmu.Lock()
	werr := writeFrameConn(c.conn, frame)
	c.wmu.Unlock()
	if werr != nil {
		c.forget(id)
		return nil, fmt.Errorf("keeper unreachable (send): %w", werr)
	}

	select {
	case resp := <-ch:
		if resp.Err != "" {
			return nil, errors.New(resp.Err)
		}
		return resp.Body, nil
	case <-ctx.Done():
		c.forget(id)
		return nil, ctx.Err()
	}
}

// forget removes a pending waiter that will never be delivered (send failed or ctx
// cancelled), so a late response is dropped rather than delivered to a stale chan.
func (c *socketKeeperClient) forget(id uint64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// callBG runs a call with the default timeout for a method that carries no caller
// context.
func (c *socketKeeperClient) callBG(method string, reqPayload any) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultKeeperCallTimeout)
	defer cancel()
	return c.call(ctx, method, reqPayload)
}

func (c *socketKeeperClient) Resolve(handle string) (string, PublicCred, error) {
	body, err := c.callBG(mResolve, resolveReq{Handle: handle})
	if err != nil {
		return "", PublicCred{}, err
	}
	var r resolveResp
	if err := gobDecode(body, &r); err != nil {
		return "", PublicCred{}, err
	}
	return r.CredID, r.Pub, nil
}

func (c *socketKeeperClient) InjectAuth(ctx context.Context, handle, credID string) (HeaderMutation, string, error) {
	body, err := c.call(ctx, mInjectAuth, injectReq{Handle: handle, CredID: credID})
	if err != nil {
		return HeaderMutation{}, "", err
	}
	var r injectResp
	if err := gobDecode(body, &r); err != nil {
		return HeaderMutation{}, "", err
	}
	return r.Mut, r.Fingerprint, nil
}

func (c *socketKeeperClient) ForceReinject(ctx context.Context, handle, credID, rejectedFingerprint string) (HeaderMutation, error) {
	body, err := c.call(ctx, mForceReinject, forceReinjectReq{Handle: handle, CredID: credID, RejectedFingerprint: rejectedFingerprint})
	if err != nil {
		return HeaderMutation{}, err
	}
	var r forceReinjectResp
	if err := gobDecode(body, &r); err != nil {
		return HeaderMutation{}, err
	}
	return r.Mut, nil
}

func (c *socketKeeperClient) RedactBody(handle string, body []byte) ([]byte, bool, int) {
	respBody, err := c.callBG(mRedactBody, redactReq{Handle: handle, Body: body})
	if err != nil {
		// Fail closed toward safety: on a dead keeper, do not forward an unscanned
		// body as if it were clean. Block it (the gateway turns blocked into a 403).
		log.Printf("broker: keeper RedactBody failed, blocking egress: %v", err)
		return body, true, 0
	}
	var r redactResp
	if err := gobDecode(respBody, &r); err != nil {
		log.Printf("broker: keeper RedactBody decode failed, blocking egress: %v", err)
		return body, true, 0
	}
	return r.Scrubbed, r.Blocked, r.Hits
}

func (c *socketKeeperClient) SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error) {
	body, err := c.callBG(mSCRAMProof, scramReq{Handle: handle, Salt: salt, Iter: iter, AuthMessage: authMessage})
	if err != nil {
		return nil, err
	}
	var r scramResp
	if err := gobDecode(body, &r); err != nil {
		return nil, err
	}
	return r.Proof, nil
}

func (c *socketKeeperClient) NeedsReauth() []string {
	body, err := c.callBG(mNeedsReauth, nil)
	if err != nil {
		log.Printf("broker: keeper NeedsReauth failed: %v", err)
		return nil
	}
	var r needsReauthResp
	if err := gobDecode(body, &r); err != nil {
		log.Printf("broker: keeper NeedsReauth decode failed: %v", err)
		return nil
	}
	return r.Keys
}

func (c *socketKeeperClient) ClearReauth(key string) {
	if _, err := c.callBG(mClearReauth, clearReauthReq{Key: key}); err != nil {
		log.Printf("broker: keeper ClearReauth failed: %v", err)
	}
}

func (c *socketKeeperClient) FlagReauth(handle string) {
	if _, err := c.callBG(mFlagReauth, flagReauthReq{Handle: handle}); err != nil {
		log.Printf("broker: keeper FlagReauth failed: %v", err)
	}
}

func (c *socketKeeperClient) SetEgressMode(mode string) {
	if _, err := c.callBG(mSetEgressMode, setEgressReq{Mode: mode}); err != nil {
		log.Printf("broker: keeper SetEgressMode failed: %v", err)
	}
}

// SetOAuthPersister is a no-op on the client: the persister writes rotated tokens
// to disk keeper-side and can't cross the gob boundary, so the keeper process
// configures its own persister at startup (see the keeper serve entrypoint). The
// method exists only to satisfy broker.Keeper.
func (c *socketKeeperClient) SetOAuthPersister(OAuthPersister) {}

// ============================ server (KEEPER side) ============================

// serveKeeper reads framed requests from conn and dispatches each to k in its own
// goroutine (so a blocking keeper op doesn't stall the stream), writing the tagged
// response back. It returns nil on a clean EOF (front gone — fail closed, the
// keeper exits) and the error on any framing failure. Concurrent responses are
// serialized onto conn by an internal write mutex.
func serveKeeper(conn net.Conn, k Keeper) error {
	var wmu sync.Mutex
	writeResp := func(resp rpcResponse) {
		frame, err := gobEncode(resp)
		if err != nil {
			// Can't encode the response envelope itself — nothing safe to send.
			log.Printf("broker keeper: response encode failed: %v", err)
			return
		}
		wmu.Lock()
		if err := writeFrameConn(conn, frame); err != nil {
			log.Printf("broker keeper: response write failed: %v", err)
		}
		wmu.Unlock()
	}

	// sem bounds concurrent dispatch; the read loop blocks past keeperInflightLimit so
	// a request flood applies socket backpressure instead of unbounded goroutines.
	sem := make(chan struct{}, keeperInflightLimit)
	for {
		frame, err := readFrameConn(conn)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // front gone between requests — exit cleanly (fail closed)
			}
			return err
		}
		var req rpcRequest
		if err := gobDecode(frame, &req); err != nil {
			return fmt.Errorf("broker keeper: request decode: %w", err)
		}
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			writeResp(handleKeeperRequest(k, req))
		}()
	}
}

// handleKeeperRequest dispatches one request and CONTAINS any panic to just this
// request — returning a fail-closed error to the single offending caller rather
// than letting the panic unwind the keeper's goroutine and crash the whole vault
// process. The untrusted front could otherwise turn one hostile-but-valid-framed
// request into a keeper-wide DoS (the in-process keeper got this recovery for free
// from net/http's per-request serve loop; serveKeeper must provide it itself).
func handleKeeperRequest(k Keeper, req rpcRequest) (resp rpcResponse) {
	resp.ID = req.ID
	defer func() {
		if r := recover(); r != nil {
			log.Printf("broker keeper: recovered panic handling %q: %v", req.Method, r)
			resp.Body = nil
			resp.Err = "keeper: internal error handling " + req.Method // no internal detail to the front
		}
	}()
	body, err := dispatchKeeper(k, req)
	resp.Body = body
	if err != nil {
		resp.Err = err.Error()
	}
	return resp
}

// dispatchKeeper decodes a request's per-method payload, invokes k, and returns the
// gob-encoded result payload (or an error). An unknown method is a clean error.
func dispatchKeeper(k Keeper, req rpcRequest) ([]byte, error) {
	switch req.Method {
	case mResolve:
		var a resolveReq
		if err := gobDecode(req.Body, &a); err != nil {
			return nil, err
		}
		credID, pub, err := k.Resolve(a.Handle)
		if err != nil {
			return nil, err
		}
		return gobEncode(resolveResp{CredID: credID, Pub: pub})
	case mInjectAuth:
		var a injectReq
		if err := gobDecode(req.Body, &a); err != nil {
			return nil, err
		}
		// The caller's context can't cross the wire; bound the keeper-side op (the
		// OAuth refresh) so a slow/hostile token endpoint can't wedge this goroutine.
		ctx, cancel := context.WithTimeout(context.Background(), keeperOpTimeout)
		defer cancel()
		mut, fp, err := k.InjectAuth(ctx, a.Handle, a.CredID)
		if err != nil {
			return nil, err
		}
		return gobEncode(injectResp{Mut: mut, Fingerprint: fp})
	case mForceReinject:
		var a forceReinjectReq
		if err := gobDecode(req.Body, &a); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), keeperOpTimeout)
		defer cancel()
		mut, err := k.ForceReinject(ctx, a.Handle, a.CredID, a.RejectedFingerprint)
		if err != nil {
			return nil, err
		}
		return gobEncode(forceReinjectResp{Mut: mut})
	case mRedactBody:
		var a redactReq
		if err := gobDecode(req.Body, &a); err != nil {
			return nil, err
		}
		scrubbed, blocked, hits := k.RedactBody(a.Handle, a.Body)
		return gobEncode(redactResp{Scrubbed: scrubbed, Blocked: blocked, Hits: hits})
	case mSCRAMProof:
		var a scramReq
		if err := gobDecode(req.Body, &a); err != nil {
			return nil, err
		}
		// iter is attacker-controllable over the wire (in-process it comes from the
		// trusted upstream DB); cap it so a compromised front can't pin a core with a
		// huge PBKDF2 count.
		if a.Iter < 1 || a.Iter > maxSCRAMIter {
			return nil, fmt.Errorf("keeper: SCRAM iteration count %d out of range", a.Iter)
		}
		proof, err := k.SCRAMProof(a.Handle, a.Salt, a.Iter, a.AuthMessage)
		if err != nil {
			return nil, err
		}
		return gobEncode(scramResp{Proof: proof})
	case mNeedsReauth:
		return gobEncode(needsReauthResp{Keys: k.NeedsReauth()})
	case mClearReauth:
		var a clearReauthReq
		if err := gobDecode(req.Body, &a); err != nil {
			return nil, err
		}
		k.ClearReauth(a.Key)
		return nil, nil
	case mFlagReauth:
		var a flagReauthReq
		if err := gobDecode(req.Body, &a); err != nil {
			return nil, err
		}
		k.FlagReauth(a.Handle)
		return nil, nil
	case mSetEgressMode:
		var a setEgressReq
		if err := gobDecode(req.Body, &a); err != nil {
			return nil, err
		}
		k.SetEgressMode(a.Mode)
		return nil, nil
	default:
		return nil, fmt.Errorf("broker keeper: unknown method %q", req.Method)
	}
}

// ============================ framing codec ============================

func gobEncode(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// gobDecode decodes a framed payload into v. The keeper decodes REQUEST payloads
// from the (post-RCE untrusted) front here, so this is untrusted deserialization —
// mitigated by: (1) a bounded frame length (readFrameConn caps at maxKeeperFrameLen),
// so decode allocation is bounded to a finite multiple of the frame size rather than
// unbounded; (2) decoding only into FIXED
// concrete structs (never an interface), so gob can't be steered to instantiate an
// arbitrary type; (3) a decode error is handled fail-closed (serveKeeper returns and
// the keeper exits — no vaultless front proceeds). FuzzKeeperServer exercises this
// path (arbitrary bytes -> envelope decode -> dispatch -> per-method decode) and
// asserts it never panics, joining the existing redactor/proxy-auth fuzzers.
//
//nolint:gosec // G709: untrusted gob decode is bounded (frame length), target-typed (fixed structs, no interface), fail-closed, and fuzzed (FuzzKeeperServer).
func gobDecode(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

// writeFrameConn writes a single length-prefixed frame ([4-byte BE len][payload])
// in one Write so concurrent frames never interleave on the stream.
func writeFrameConn(w io.Writer, payload []byte) error {
	if len(payload) > maxKeeperFrameLen {
		return fmt.Errorf("keeper frame of %d bytes exceeds max %d", len(payload), maxKeeperFrameLen)
	}
	buf := make([]byte, 4+len(payload))
	//nolint:gosec // G115: len(payload) is bounded by maxKeeperFrameLen above, so the int->uint32 conversion cannot overflow.
	binary.BigEndian.PutUint32(buf[:4], uint32(len(payload)))
	copy(buf[4:], payload)
	_, err := w.Write(buf)
	return err
}

func readFrameConn(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxKeeperFrameLen {
		return nil, fmt.Errorf("keeper frame length %d exceeds max %d", n, maxKeeperFrameLen)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
