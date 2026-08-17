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
	"net"
	"net/http"
	"sync"
	"time"

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
	mu             sync.Mutex
	pods           map[string][]string
	events         []string // recent autoscale activity (bounded ring), for `daemon status`
	l4Redis        net.Listener
	l4RedisAddr    string
	l4Postgres     net.Listener
	l4PostgresAddr string
}

// maxEvents bounds the autoscale event ring surfaced in `daemon status`.
const maxEvents = 50

// recordEvent appends a timestamped autoscale event, keeping the ring bounded.
// The autoscaler feeds it through its Log/Warn hooks; `daemon status` shows it.
func (d *Daemon) recordEvent(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, msg)
	if len(d.events) > maxEvents {
		d.events = d.events[len(d.events)-maxEvents:]
	}
}

// New returns a Daemon wrapping b.
func New(b brokerAPI) *Daemon { return &Daemon{broker: b, pods: map[string][]string{}} }

// Start sets the (daemon-global) egress mode, binds the injecting HTTP gateway
// (returning its address), and — when their bind addresses are non-empty — the
// L4 Redis and Postgres listeners pods reach for datastore access.
func (d *Daemon) Start(gatewayBind, egress, l4RedisBind, l4PostgresBind string) (string, error) {
	d.broker.SetEgressMode(egress)
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
	return l4.TargetFromDSN(cred.BaseURL)
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
		d.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"handle": h.Value})
	})
	mux.HandleFunc("DELETE /pods/{pod}", func(w http.ResponseWriter, r *http.Request) {
		pod := r.PathValue("pod")
		d.mu.Lock()
		handles := d.pods[pod]
		delete(d.pods, pod)
		d.mu.Unlock()
		for _, h := range handles {
			d.broker.Revoke(h)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
