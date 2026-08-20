package broker

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Server binds a Gateway to a TCP address and serves it. Serve binds
// synchronously — so a bind error and the concrete bound address are known up
// front — and serves in the background; Stop shuts it down gracefully.
type Server struct {
	gw   *Gateway
	http *http.Server
	ln   net.Listener
}

// NewServer returns a Server for the given gateway.
func NewServer(gw *Gateway) *Server { return &Server{gw: gw} }

// Serve binds to addr and starts serving in the background, returning the
// concrete bound address (e.g. "127.0.0.1:53421" when addr uses port 0). The
// caller chooses the interface; container reachability is wired at up/down.
func (s *Server) Serve(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	s.ln = ln
	s.http = &http.Server{Handler: s.gw, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.http.Serve(ln) }()
	return ln.Addr().String(), nil
}

// Addr returns the bound address, or "" if the server has not been served.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Stop gracefully shuts the server down, waiting on ctx for in-flight requests.
// It is safe to call on a Server that was never served.
func (s *Server) Stop(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}
