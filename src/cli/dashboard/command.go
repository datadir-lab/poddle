// Package dashboard implements `poddle dashboard`: a local web view of the audit
// log. It serves an embedded static bundle and proxies the /v1/audit* contract
// to the running daemon over its Unix socket. The SAME bundle + contract are
// reused by the enterprise cloud collector — only the data source differs.
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/poddled"
	"github.com/datadir-lab/poddle/src/internal/policy"
)

// Handler serves the embedded dashboard bundle at / and the versioned /v1
// contract: /v1/audit* is proxied to the daemon on the Unix socket at sock, and
// /v1/policies* is served from the policy store (so the UI can view + edit
// policies). The same contract is reused by the cloud collector — only the
// backends differ.
func Handler(sock string, policies policy.Store) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "unix"
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/v1") // /v1/audit -> /audit
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
		FlushInterval: -1, // flush immediately so /audit/stream (SSE) streams live
	}

	sub, err := fs.Sub(bundle, "dist")
	if err != nil {
		panic(err) // embedded dist is a build invariant
	}

	mux := http.NewServeMux()
	// /v1/policies* is served locally from the policy store; more specific
	// patterns win over the /v1/ proxy in Go 1.22's mux.
	if policies != nil {
		registerPolicyAPI(mux, policies)
	}
	mux.Handle("/v1/", proxy) // /v1/audit* -> daemon
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

// registerPolicyAPI wires the /v1/policies CRUD contract onto mux, backed by the
// policy store. The cloud collector serves the same routes over Postgres.
func registerPolicyAPI(mux *http.ServeMux, policies policy.Store) {
	mux.HandleFunc("GET /v1/policies", func(w http.ResponseWriter, r *http.Request) {
		names, err := policies.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]*policy.Policy, 0, len(names))
		for _, n := range names {
			if p, err := policies.Get(n); err == nil {
				out = append(out, p)
			}
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("GET /v1/policies/{name}", func(w http.ResponseWriter, r *http.Request) {
		p, err := policies.Get(r.PathValue("name"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, p)
	})
	mux.HandleFunc("PUT /v1/policies/{name}", func(w http.ResponseWriter, r *http.Request) {
		var p policy.Policy
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		p.Name = r.PathValue("name") // the URL is authoritative
		if err := policies.Put(&p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /v1/policies/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := policies.Delete(r.PathValue("name")); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// NewCmd builds `poddle dashboard`.
func NewCmd() *cobra.Command {
	var port int
	var open bool
	c := &cobra.Command{
		Use:   "dashboard",
		Short: "Serve a local web dashboard of the audit log",
		Long: "Serve a local, read-only web view of the daemon's audit log (proxied\n" +
			"requests, redactions, blocks, handle + pod lifecycle) with a live feed\n" +
			"and hash-chain verification. Local-only (binds 127.0.0.1).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("bind %s: %w", addr, err)
			}
			url := "http://" + ln.Addr().String()
			fmt.Fprintf(cmd.OutOrStdout(), "poddle dashboard: %s  (Ctrl-C to stop)\n", url)
			if open {
				_ = openBrowser(url) // best-effort
			}
			return http.Serve(ln, Handler(poddled.SocketPath(), policyStore()))
		},
	}
	c.Flags().IntVar(&port, "port", 7333, "local port to bind (127.0.0.1)")
	c.Flags().BoolVar(&open, "open", false, "open the dashboard in your browser")
	return c
}

// policyStore is the same layered policy store the CLI uses: the repo's
// poddle/policies/ shadows the global dir; writes (UI edits) go to global.
func policyStore() policy.Store {
	cwd, _ := os.Getwd()
	return policy.Layered{
		Sources: []policy.Store{policy.NewFileStore(policy.ProjectDir(cwd)), policy.NewFileStore(policy.DefaultDir())},
		Writer:  policy.NewFileStore(policy.DefaultDir()),
	}
}

// openBrowser best-effort opens url in the platform browser.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
