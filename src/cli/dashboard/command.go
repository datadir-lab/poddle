// Package dashboard implements `poddle dashboard`: a local web view of the audit
// log. It serves an embedded static bundle and proxies the /v1/audit* contract
// to the running daemon over its Unix socket. The SAME bundle + contract are
// reused by the enterprise cloud collector — only the data source differs.
package dashboard

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/poddled"
)

// Handler serves the embedded dashboard bundle at / and proxies the versioned
// audit contract (/v1/audit, /v1/audit/stream, /v1/audit/verify) to the daemon
// listening on the Unix socket at sock. The /v1 prefix is the stable, reusable
// contract; locally it maps to the daemon's /audit* routes.
func Handler(sock string) http.Handler {
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
	mux.Handle("/v1/", proxy)
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
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
			return http.Serve(ln, Handler(poddled.SocketPath()))
		},
	}
	c.Flags().IntVar(&port, "port", 7333, "local port to bind (127.0.0.1)")
	c.Flags().BoolVar(&open, "open", false, "open the dashboard in your browser")
	return c
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
