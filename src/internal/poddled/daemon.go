// Package poddled is the persistent host-side broker: it wraps a broker.Broker
// so credentials and handles outlive a single CLI invocation, exposing a small
// HTTP control API (served over a Unix socket in production) that `up`/`down`
// drive. Pods keep working — and stay reattachable — after the client exits.
//
// Egress redaction is daemon-global in this MVP: it is set once at Start (before
// the gateway serves) to avoid racing the live gateway; per-pod egress needs a
// per-pod gateway (later).
package poddled

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/l4"
	"github.com/datadir-lab/poddle/src/internal/policy"
	"github.com/datadir-lab/poddle/src/internal/tlsca"
)

// brokerAPI is the broker capability the daemon wraps; *broker.Broker satisfies it.
type brokerAPI interface {
	Store(broker.Credential) (string, error)
	IssueHandle(credID, scope string, ttl time.Duration) (broker.Handle, error)
	Revoke(handleValue string)
	ResolveDatastore(handleValue string) (l4.Target, error)
	Serve(addr string) (string, error)
	Addr() string
	SetEgressMode(mode string)
	Stop(ctx context.Context) error

	// EnsureCA loads the egress-interception CA keeper-side (the CA private key
	// stays in the keeper, not this front); LeafSource returns a forward-proxy
	// LeafSource that mints leaves via the keeper. Wired when the forward proxy
	// starts, if interception is available.
	EnsureCA(dir string) error
	LeafSource() broker.LeafSource

	// SCRAMProof is the L4 Postgres SCRAM password-bearing step, delegated to
	// the broker's keeper (see broker.Keeper.SCRAMProof); this method makes
	// brokerAPI satisfy l4.SCRAMKeeper structurally, so d.broker itself can be
	// handed to l4.ServePostgres as the L4 Postgres terminator's keeper.
	SCRAMProof(handle string, salt []byte, iter int, authMessage string) ([]byte, error)
}

// Daemon is the persistent broker plus a pod→handles registry (so `down` can
// revoke everything a pod was issued) and the L4 datastore listeners.
type Daemon struct {
	broker         brokerAPI
	audit          *audit.Store // tamper-evident audit log (nil = auditing off, e.g. in tests)
	mu             sync.Mutex
	pods           map[string][]string
	handlePod      map[string]string         // handle value -> pod, so the gateway resolves a pod
	podPolicy      map[string]*policy.Policy // pod -> its governance policy (nil = unrestricted)
	events         []string                  // recent autoscale activity (bounded ring), for `daemon status`
	l4Redis        net.Listener
	l4RedisAddr    string
	l4Postgres     net.Listener
	l4PostgresAddr string
	forward        net.Listener // egress forward proxy (arbitrary HTTP(S) egress)
	forwardAddr    string
	loopbackHost   string // if set, a loopback upstream is dialed here (the host route); see broker.RewriteLoopbackHost
	mirrorDir      string // OAuthMirrorDir(): where the broker durably writes rotated OAuth material; GET /oauth/mirror reads it
	brokerPrivsep  bool   // broker running two-process (PODDLE_BROKER_PRIVSEP); surfaced in `daemon status`

	// Fresh-audit egress gate (opt-in via PODDLE_REQUIRE_FRESH_AUDIT). When on,
	// Check denies egress unless the audit was acked within maxStaleness.
	requireFreshAudit bool
	maxStaleness      time.Duration
	lastAck           int64     // cloud acked_through (informational)
	lastAckAt         time.Time // daemon receive time of the last ack (zero = never)
}

// maxEvents bounds the autoscale event ring surfaced in `daemon status`.
const maxEvents = 50

// recordEvent appends a timestamped autoscale event, keeping the ring bounded
// (for `daemon status`), and mirrors it into the audit log.
func (d *Daemon) recordEvent(msg string) {
	d.mu.Lock()
	d.events = append(d.events, msg)
	if len(d.events) > maxEvents {
		d.events = d.events[len(d.events)-maxEvents:]
	}
	d.mu.Unlock()

	kind := audit.KindAutoscaleWarn
	if strings.Contains(msg, "grew") {
		kind = audit.KindAutoscaleGrow
	}
	d.rec(audit.Event{Kind: kind, Detail: msg, Decision: audit.DecisionAllow})
}

// New returns a Daemon wrapping b, recording audit events to aud (nil = off).
func New(b brokerAPI, aud *audit.Store) *Daemon {
	return &Daemon{
		broker: b, audit: aud,
		pods: map[string][]string{}, handlePod: map[string]string{},
		podPolicy: map[string]*policy.Policy{},
		mirrorDir: OAuthMirrorDir(),
	}
}

// SetLoopbackHost configures the host route a loopback upstream is dialed at, so
// a locked pod's local datastore (a Postgres/Redis at 127.0.0.1, or a local HTTP
// service) reaches the *host* rather than the broker container's own loopback.
// Empty (the default) disables the rewrite. Call before Start.
func (d *Daemon) SetLoopbackHost(h string) { d.loopbackHost = h }

// SetBrokerPrivsep records whether the broker is running two-process (Tier-2
// privilege separation), so `daemon status` can report it. Call before Start.
func (d *Daemon) SetBrokerPrivsep(v bool) { d.brokerPrivsep = v }

// Check implements broker.PolicyChecker: resolve the pod holding handle and
// evaluate its policy for (host, method). No policy = allow.
func (d *Daemon) Check(handle, host, method string) (bool, string) {
	d.mu.Lock()
	pod, known := d.handlePod[handle]
	pol := d.podPolicy[pod]
	stale := d.requireFreshAudit && (d.lastAckAt.IsZero() || time.Since(d.lastAckAt) > d.maxStaleness)
	staleness := d.maxStaleness
	d.mu.Unlock()
	// An empty or unrecognized egress token maps to no pod, hence no governing
	// policy — deny rather than fall through to podPolicy[""] == nil == allow.
	// Every brokered pod is handed its own egress token, so a legitimate request
	// always carries one; a policy pod that strips it must not thereby escape its
	// own allow-list. (nil pol below = a known pod with no policy = default-allow.)
	if !known {
		return false, "unrecognized egress token"
	}
	if stale {
		return false, "audit not fresh (fail-closed): no cloud ack within " + staleness.String()
	}
	return pol.Decide(host, method)
}

// Monitored implements broker.MonitorChecker: the pod's policy is in monitor
// mode, so a would-be denial should be logged (not blocked).
func (d *Daemon) Monitored(handle string) bool {
	d.mu.Lock()
	pol := d.podPolicy[d.handlePod[handle]]
	d.mu.Unlock()
	return pol != nil && pol.Monitor
}

// Intercepts implements broker.InterceptChecker: whether the pod's HTTPS egress
// to host should be TLS-terminated (per-host intercept_hosts, or the legacy
// intercept bool for all hosts).
func (d *Daemon) Intercepts(handle, host string) bool {
	d.mu.Lock()
	pol := d.podPolicy[d.handlePod[handle]]
	d.mu.Unlock()
	return pol.InterceptsHost(host)
}

// EgressMode implements broker.EgressModer: the pod policy's egress redaction
// mode ("redact" | "block" | "off"), empty when the pod names no policy — so an
// intercepting pod's HTTPS request bodies are scrubbed per its policy.
func (d *Daemon) EgressMode(handle string) string {
	d.mu.Lock()
	pol := d.podPolicy[d.handlePod[handle]]
	d.mu.Unlock()
	if pol == nil {
		return ""
	}
	return pol.Egress
}

// rec appends a sanitised audit event if auditing is on.
func (d *Daemon) rec(e audit.Event) {
	if d.audit != nil {
		_, _ = d.audit.Append(audit.NewEvent(e))
	}
}

// Proxy implements broker.Auditor: one record per proxied request. It resolves
// the pod from the presented handle (the daemon owns that mapping) and records a
// request event with the allow/redact/block decision.
func (d *Daemon) Proxy(r broker.ProxyRecord) {
	d.mu.Lock()
	pod := d.handlePod[r.Handle]
	d.mu.Unlock()
	d.rec(audit.Event{
		Pod: pod, Kind: audit.KindRequest, Upstream: r.Upstream, Method: r.Method,
		Path: r.Path, Status: r.Status, Decision: audit.Decision(r.Decision), Detail: r.Detail,
	})
}

// Start sets the (daemon-global) egress mode, binds the injecting HTTP gateway
// (returning its address), and — when their bind addresses are non-empty — the
// L4 Redis and Postgres listeners pods reach for datastore access.
func (d *Daemon) Start(gatewayBind, egress, l4RedisBind, l4PostgresBind, forwardBind string) (string, error) {
	d.broker.SetEgressMode(egress)
	// Opt-in fail-closed gate (default off): deny egress unless the audit was
	// acked within PODDLE_MAX_AUDIT_STALENESS (default 5m).
	if v := os.Getenv("PODDLE_REQUIRE_FRESH_AUDIT"); v == "1" || strings.EqualFold(v, "true") {
		d.requireFreshAudit = true
		d.maxStaleness = 5 * time.Minute
		if s := os.Getenv("PODDLE_MAX_AUDIT_STALENESS"); s != "" {
			if dur, err := time.ParseDuration(s); err == nil && dur > 0 {
				d.maxStaleness = dur
			}
		}
	}
	if a, ok := d.broker.(interface{ SetAuditor(broker.Auditor) }); ok {
		a.SetAuditor(d) // audit every proxied request
	}
	if p, ok := d.broker.(interface{ SetPolicyChecker(broker.PolicyChecker) }); ok {
		p.SetPolicyChecker(d) // enforce each pod's governance policy
	}
	if lb, ok := d.broker.(interface{ SetLoopbackHost(string) }); ok {
		lb.SetLoopbackHost(d.loopbackHost) // dial loopback upstreams at the host route
	}
	addr, err := d.broker.Serve(gatewayBind)
	if err != nil {
		return "", err
	}
	if l4RedisBind != "" {
		ln, err := net.Listen("tcp", l4RedisBind)
		if err != nil {
			return "", err
		}
		d.l4Redis = ln
		d.l4RedisAddr = ln.Addr().String()
		go d.accept(ln, l4.ServeRedis)
	}
	if l4PostgresBind != "" {
		ln, err := net.Listen("tcp", l4PostgresBind)
		if err != nil {
			return "", err
		}
		d.l4Postgres = ln
		d.l4PostgresAddr = ln.Addr().String()
		// d.broker satisfies l4.SCRAMKeeper (its SCRAMProof method), so the L4
		// Postgres terminator's SCRAM step delegates to the broker keeper instead
		// of computing the proof from a locally-held password — see
		// docs/design/broker-privilege-separation.md.
		go d.accept(ln, func(c net.Conn, r l4.Resolver) error {
			return l4.ServePostgres(c, r, d.broker)
		})
	}
	if forwardBind != "" {
		ln, err := net.Listen("tcp", forwardBind)
		if err != nil {
			return "", err
		}
		d.forward = ln
		d.forwardAddr = ln.Addr().String()
		fp := broker.NewForwardProxy(d, d) // d is PolicyChecker + Auditor
		fp.SetLoopbackHost(d.loopbackHost) // dial loopback destinations at the host route
		// Load the egress-interception CA so opted-in pods' HTTPS can be inspected.
		// The containerized broker is pointed at its bind-mounted state dir
		// (PODDLE_EGRESS_CA_DIR=/state/egress-ca) so the CA it signs leaves with is
		// the SAME file `up` reads to inject into a pod's trust store — and it
		// persists across broker restarts. Falls back to the user config dir for a
		// bare-host daemon / tests. Best-effort: on failure, interception is simply
		// unavailable (opaque tunnel).
		caDir := os.Getenv("PODDLE_EGRESS_CA_DIR")
		if caDir == "" {
			caDir = tlsca.DefaultDir()
		}
		// Load the CA KEEPER-SIDE — the CA private key that signs every leaf lives in
		// the keeper (in two-process mode, a separate process), never in this front —
		// and mint leaves via the keeper's SignLeaf. Best-effort: on failure,
		// interception is simply unavailable (opaque tunnel); log it (secret-free) so
		// an operator can tell why an intercept-policy pod falls back to tunnelling.
		if err := d.broker.EnsureCA(caDir); err == nil {
			fp.SetLeafSource(d.broker.LeafSource())
		} else {
			log.Printf("poddled: egress-interception CA unavailable (%v); intercept policies fall back to opaque tunnel", err)
		}
		fsrv := &http.Server{Handler: fp, ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = fsrv.Serve(ln) }()
	}
	return addr, nil
}

// accept serves each accepted connection with the given L4 handler.
func (d *Daemon) accept(ln net.Listener, serve func(net.Conn, l4.Resolver) error) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() { _ = serve(conn, d) }()
	}
}

// Resolve implements l4.Resolver: a pod-presented handle → its real datastore
// Target (parsed from the credential's DSN).
func (d *Daemon) Resolve(handle string) (l4.Target, error) {
	// The broker resolves the handle to a datastore Target keeper-side (parsing the
	// DSN there), so an OAuth handle's refresh token never crosses into this front.
	t, err := d.broker.ResolveDatastore(handle)
	if err != nil {
		return l4.Target{}, err
	}
	// A loopback datastore (127.0.0.1/localhost) means the host's loopback, not this
	// container's; dial it at the host route. Governance is unchanged (L4 has no host
	// allow-list); the audit records the actually-dialed addr.
	t.Addr = broker.RewriteLoopbackHost(t.Addr, d.loopbackHost)
	d.mu.Lock()
	pod := d.handlePod[handle]
	d.mu.Unlock()
	d.rec(audit.Event{Pod: pod, Kind: audit.KindL4Connect, Upstream: t.Addr, Decision: audit.DecisionAllow})
	return t, nil
}

// Stop shuts the gateway, L4, and forward-proxy listeners down.
func (d *Daemon) Stop(ctx context.Context) error {
	if d.l4Redis != nil {
		_ = d.l4Redis.Close()
	}
	if d.l4Postgres != nil {
		_ = d.l4Postgres.Close()
	}
	if d.forward != nil {
		_ = d.forward.Close()
	}
	return d.broker.Stop(ctx)
}

// randHex returns n random bytes as a hex string, for opaque egress tokens.
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// issueReq is the body of POST /pods/{pod}/handles.
type issueReq struct {
	Scope      string            `json:"scope"`
	Credential broker.Credential `json:"credential"`
}

// Status is what GET /status reports: the pod-facing addresses and the active
// pods with their live handle counts.
type Status struct {
	Gateway     string         `json:"gateway"`
	Redis       string         `json:"redis"`
	Postgres    string         `json:"postgres"`
	Pods        map[string]int `json:"pods"`
	Events      []string       `json:"events"`       // recent autoscale activity (grows, warnings)
	NeedsReauth []string       `json:"needs_reauth"` // connection names whose OAuth refresh failed (broker.Broker.NeedsReauth) — secretless, names only
	// BrokerPrivsep reports whether the broker is running two-process (Tier-2
	// privilege separation, PODDLE_BROKER_PRIVSEP=1): a keeper subprocess holds the
	// vault while this front parses untrusted bytes. A running daemon reporting true
	// inherently means the keeper is live — if it died, the daemon would have exited.
	BrokerPrivsep bool `json:"broker_privsep"`
}

// Handler returns the control API:
//
//	GET    /health                 liveness
//	GET    /gateway                {"addr": pod-facing gateway address}
//	POST   /pods/{pod}/handles     store a credential + issue a handle for pod
//	DELETE /pods/{pod}             revoke every handle issued for pod
//	GET    /oauth/mirror           drain the durable OAuth mirror files (raw JSON per connection)
func (d *Daemon) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /gateway", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"addr": d.broker.Addr(), "redis": d.l4RedisAddr,
			"postgres": d.l4PostgresAddr, "forward": d.forwardAddr,
		})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		pods := make(map[string]int, len(d.pods))
		for name, handles := range d.pods {
			pods[name] = len(handles)
		}
		events := append([]string(nil), d.events...)
		d.mu.Unlock()
		// NeedsReauth is an OPTIONAL broker capability (mirrors SetAuditor/
		// SetPolicyChecker above): a type-assertion so existing brokerAPI test
		// doubles that don't implement it need no change — absent capability just
		// reports no flagged connections.
		var needsReauth []string
		if nr, ok := d.broker.(interface{ NeedsReauth() []string }); ok {
			needsReauth = nr.NeedsReauth()
		}
		writeJSON(w, http.StatusOK, Status{
			Gateway: d.broker.Addr(), Redis: d.l4RedisAddr, Postgres: d.l4PostgresAddr,
			Pods: pods, Events: events, NeedsReauth: needsReauth,
			BrokerPrivsep: d.brokerPrivsep,
		})
	})
	mux.HandleFunc("POST /pods/{pod}/handles", func(w http.ResponseWriter, r *http.Request) {
		var req issueReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		credID, err := d.broker.Store(req.Credential)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h, err := d.broker.IssueHandle(credID, req.Scope, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pod := r.PathValue("pod")
		d.mu.Lock()
		d.pods[pod] = append(d.pods[pod], h.Value)
		d.handlePod[h.Value] = pod
		d.mu.Unlock()
		d.rec(audit.Event{Pod: pod, Kind: audit.KindHandleIssue, Detail: "scope=" + req.Scope, Decision: audit.DecisionAllow})
		writeJSON(w, http.StatusOK, map[string]string{"handle": h.Value})
	})
	mux.HandleFunc("DELETE /pods/{pod}", func(w http.ResponseWriter, r *http.Request) {
		pod := r.PathValue("pod")
		d.mu.Lock()
		handles := d.pods[pod]
		delete(d.pods, pod)
		delete(d.podPolicy, pod)
		for _, h := range handles {
			delete(d.handlePod, h)
		}
		d.mu.Unlock()
		for _, h := range handles {
			d.broker.Revoke(h)
		}
		if len(handles) > 0 {
			d.rec(audit.Event{Pod: pod, Kind: audit.KindHandleRevoke, Detail: fmt.Sprintf("%d handle(s)", len(handles)), Decision: audit.DecisionAllow})
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /pods/{pod}/egress", func(w http.ResponseWriter, r *http.Request) {
		pod := r.PathValue("pod")
		tok := "poddle_egr_" + randHex(16)
		d.mu.Lock()
		d.pods[pod] = append(d.pods[pod], tok) // tracked so `down` clears it
		d.handlePod[tok] = pod                 // so the forward proxy resolves pod->policy
		d.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"token": tok, "addr": d.forwardAddr})
	})

	mux.HandleFunc("POST /pods/{pod}/policy", func(w http.ResponseWriter, r *http.Request) {
		var p policy.Policy
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		pod := r.PathValue("pod")
		d.mu.Lock()
		d.podPolicy[pod] = &p
		d.mu.Unlock()
		d.rec(audit.Event{Pod: pod, Kind: audit.KindPolicyAllow, Detail: "policy " + p.Name + " bound", Decision: audit.DecisionAllow})
		w.WriteHeader(http.StatusNoContent)
	})
	// The effective (in-memory) policy bound to each pod. Container labels are
	// immutable, so a rebind cannot change poddle.policy on a running pod — this
	// is how the dashboard's pods list reflects the current binding instead of the
	// stale label it was created with.
	mux.HandleFunc("GET /pods/policies", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		out := make(map[string]string, len(d.podPolicy))
		for pod, p := range d.podPolicy {
			if p != nil && p.Name != "" {
				out[pod] = p.Name
			}
		}
		d.mu.Unlock()
		writeJSON(w, http.StatusOK, out)
	})

	// Host-pushed autoscale events: the autoscaler now runs on the host (it
	// shells podman / `poddle move`, which the broker container cannot), so it
	// POSTs its grow/warn activity here to keep `daemon status` and the audit
	// log complete.
	mux.HandleFunc("POST /events", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Msg string `json:"msg"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		d.recordEvent(body.Msg)
		w.WriteHeader(http.StatusNoContent)
	})

	// Report an audit-sync watermark: an external syncer (the cloud agent) posts
	// how far the durable copy has been acked. Generic — the daemon stamps its own
	// receive time and never trusts a caller clock. Feeds the fresh-audit gate.
	mux.HandleFunc("POST /audit/ack", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			AckedThrough int64 `json:"acked_through"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		d.mu.Lock()
		d.lastAck = body.AckedThrough
		d.lastAckAt = time.Now()
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	// Audit control API.
	mux.HandleFunc("POST /audit", func(w http.ResponseWriter, r *http.Request) {
		var e audit.Event
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		d.rec(e) // NewEvent inside rec strips any query-string / reduces upstream to host
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /audit", func(w http.ResponseWriter, r *http.Request) {
		if d.audit == nil {
			writeJSON(w, http.StatusOK, []audit.Event{})
			return
		}
		q := r.URL.Query()
		since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		events, err := d.audit.Query(audit.Filter{
			Pod: q.Get("pod"), Kind: q.Get("kind"), Decision: q.Get("decision"),
			SinceSeq: since, Limit: limit,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if events == nil {
			events = []audit.Event{}
		}
		writeJSON(w, http.StatusOK, events)
	})
	// GET /oauth/mirror drains the durable OAuth mirror files the gateway
	// persists (Task 3) over the control socket: poddled is the only side of
	// the container boundary that can read its own bind-mounted state dir, and
	// the host needs the rotated material to reconcile into connections/oauth.json
	// (Task 6). Passed through as raw per-connection JSON — poddled deliberately
	// does not import the connector package (no third copy of its OAuth-material
	// json tags); the host side unmarshals into connector.OAuthMaterial. Never
	// logged: this endpoint serves the host's own tokens over the 0600
	// owner-only socket.
	mux.HandleFunc("GET /oauth/mirror", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]json.RawMessage{}
		entries, err := os.ReadDir(d.mirrorDir)
		if err != nil {
			if !os.IsNotExist(err) {
				// Secret-free: report the failure shape, never file contents.
				http.Error(w, "read oauth mirror dir", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(d.mirrorDir, name))
			if err != nil || !json.Valid(b) {
				continue // skip unreadable/invalid files rather than fail the whole request
			}
			out[strings.TrimSuffix(name, ".json")] = json.RawMessage(b)
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("GET /audit/verify", func(w http.ResponseWriter, r *http.Request) {
		if d.audit == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "brokenAt": int64(0)})
			return
		}
		ok, at, err := d.audit.Verify()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "brokenAt": at})
	})
	mux.HandleFunc("GET /audit/stream", func(w http.ResponseWriter, r *http.Request) {
		if d.audit == nil {
			http.Error(w, "auditing off", http.StatusServiceUnavailable)
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		fl.Flush()
		ch, cancel := d.audit.Subscribe()
		defer cancel()
		enc := json.NewEncoder(w)
		for {
			select {
			case <-r.Context().Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				_, _ = w.Write([]byte("data: "))
				_ = enc.Encode(e) // writes the JSON + a newline
				_, _ = w.Write([]byte("\n"))
				fl.Flush()
			}
		}
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
