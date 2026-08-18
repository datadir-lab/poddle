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
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.dev.datadir.co/datadir/poddle/src/internal/audit"
	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/l4"
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
	handlePod      map[string]string // handle value -> pod, so the gateway's audit records resolve a pod
	events         []string          // recent autoscale activity (bounded ring), for `daemon status`
	l4Redis        net.Listener
	l4RedisAddr    string
	l4Postgres     net.Listener
	l4PostgresAddr string
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
	return &Daemon{broker: b, audit: aud, pods: map[string][]string{}, handlePod: map[string]string{}}
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
func (d *Daemon) Start(gatewayBind, egress, l4RedisBind, l4PostgresBind string) (string, error) {
	d.broker.SetEgressMode(egress)
	if a, ok := d.broker.(interface{ SetAuditor(broker.Auditor) }); ok {
		a.SetAuditor(d) // audit every proxied request
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

// Stop shuts the gateway and L4 listeners down.
func (d *Daemon) Stop(ctx context.Context) error {
	if d.l4Redis != nil {
		_ = d.l4Redis.Close()
	}
	if d.l4Postgres != nil {
		_ = d.l4Postgres.Close()
	}
	return d.broker.Stop(ctx)
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
		writeJSON(w, http.StatusOK, map[string]string{"addr": d.broker.Addr(), "redis": d.l4RedisAddr, "postgres": d.l4PostgresAddr})
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
