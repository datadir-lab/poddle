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
	"net/http"
	"sync"
	"time"

	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
)

// brokerAPI is the broker capability the daemon wraps; *broker.Broker satisfies it.
type brokerAPI interface {
	Store(broker.Credential) (string, error)
	IssueHandle(credID, scope string, ttl time.Duration) (broker.Handle, error)
	Revoke(handleValue string)
	Serve(addr string) (string, error)
	Addr() string
	SetEgressMode(mode string)
	Stop(ctx context.Context) error
}

// Daemon is the persistent broker plus a pod→handles registry, so `down` can
// revoke everything a pod was issued.
type Daemon struct {
	broker brokerAPI
	mu     sync.Mutex
	pods   map[string][]string
}

// New returns a Daemon wrapping b.
func New(b brokerAPI) *Daemon { return &Daemon{broker: b, pods: map[string][]string{}} }

// Start sets the (daemon-global) egress mode and binds the injecting gateway
// that pods reach, returning its address.
func (d *Daemon) Start(gatewayBind, egress string) (string, error) {
	d.broker.SetEgressMode(egress)
	return d.broker.Serve(gatewayBind)
}

// Stop shuts the gateway down.
func (d *Daemon) Stop(ctx context.Context) error { return d.broker.Stop(ctx) }

// issueReq is the body of POST /pods/{pod}/handles.
type issueReq struct {
	Scope      string            `json:"scope"`
	Credential broker.Credential `json:"credential"`
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
		writeJSON(w, http.StatusOK, map[string]string{"addr": d.broker.Addr()})
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
