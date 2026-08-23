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
	"net"
	"net/http"
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
	Resolve(handleValue string) (broker.Credential, error)
	Serve(addr string) (string, error)
	Addr() string
	SetEgressMode(mode string)
	Stop(ctx context.Context) error
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
	ca             *tlsca.Authority // egress-interception CA (nil until the forward proxy starts)
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
	}
}

// Check implements broker.PolicyChecker: resolve the pod holding handle and
// evaluate its policy for (host, method). No policy = allow.
func (d *Daemon) Check(handle, host, method string) (bool, string) {
	d.mu.Lock()
	pod, known := d.handlePod[handle]
	pol := d.podPolicy[pod]
	d.mu.Unlock()
	// An empty or unrecognized egress token maps to no pod, hence no governing
	// policy — deny rather than fall through to podPolicy[""] == nil == allow.
	// Every brokered pod is handed its own egress token, so a legitimate request
	// always carries one; a policy pod that strips it must not thereby escape its
	// own allow-list. (nil pol below = a known pod with no policy = default-allow.)
	if !known {
		return false, "unrecognized egress token"
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

// Intercepts implements broker.InterceptChecker: the pod's policy opts into TLS
// interception, so its HTTPS egress should be terminated (not tunnelled).
func (d *Daemon) Intercepts(handle string) bool {
	d.mu.Lock()
	pol := d.podPolicy[d.handlePod[handle]]
	d.mu.Unlock()
	return pol != nil && pol.Intercept
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
	if a, ok := d.broker.(interface{ SetAuditor(broker.Auditor) }); ok {
		a.SetAuditor(d) // audit every proxied request
	}
	if p, ok := d.broker.(interface{ SetPolicyChecker(broker.PolicyChecker) }); ok {
		p.SetPolicyChecker(d) // enforce each pod's governance policy
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
		go d.accept(ln, l4.ServePostgres)
	}
	if forwardBind != "" {
		ln, err := net.Listen("tcp", forwardBind)
		if err != nil {
			return "", err
		}
		d.forward = ln
		d.forwardAddr = ln.Addr().String()
		fp := broker.NewForwardProxy(d, d) // d is PolicyChecker + Auditor
		// Load the egress-interception CA so opted-in pods' HTTPS can be inspected.
		// Best-effort: on failure, interception is simply unavailable (opaque tunnel).
		if ca, err := tlsca.Load(tlsca.DefaultDir()); err == nil {
			d.ca = ca
			fp.SetLeafSource(ca)
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
	cred, err := d.broker.Resolve(handle)
	if err != nil {
		return l4.Target{}, err
	}
	t, err := l4.TargetFromDSN(cred.BaseURL)
	if err == nil {
		d.mu.Lock()
		pod := d.handlePod[handle]
		d.mu.Unlock()
		d.rec(audit.Event{Pod: pod, Kind: audit.KindL4Connect, Upstream: t.Addr, Decision: audit.DecisionAllow})
	}
	return t, err
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
	Gateway  string         `json:"gateway"`
	Redis    string         `json:"redis"`
	Postgres string         `json:"postgres"`
	Pods     map[string]int `json:"pods"`
	Events   []string       `json:"events"` // recent autoscale activity (grows, warnings)
}

// Handler returns the control API:
//
//	GET    /health                 liveness
//	GET    /gateway                {"addr": pod-facing gateway address}
//	POST   /pods/{pod}/handles     store a credential + issue a handle for pod
//	DELETE /pods/{pod}             revoke every handle issued for pod
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
		writeJSON(w, http.StatusOK, Status{
			Gateway: d.broker.Addr(), Redis: d.l4RedisAddr, Postgres: d.l4PostgresAddr,
			Pods: pods, Events: events,
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
