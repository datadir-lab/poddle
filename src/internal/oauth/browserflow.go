package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
)

// OpenBrowser is the default browser opener used by AuthCodeFlow's callers.
// It is a package var (rather than baked into AuthCodeFlow) so tests can
// swap in a headless opener; AuthCodeFlow itself takes `open` as a param.
var OpenBrowser = openBrowserDefault

// openBrowserDefault best-effort opens url in the platform browser. Mirrors
// dashboard/command.go's openBrowser.
func openBrowserDefault(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// AuthCodeFlow runs the browser-based OAuth 2.1 authorization-code + PKCE
// flow: it starts a one-shot 127.0.0.1 callback listener, builds the
// authorization URL, hands it to open (e.g. OpenBrowser, or a headless
// stand-in in tests), and blocks until the browser redirects back with a
// code — or ctx is done. It returns the raw code plus the redirectURI and
// PKCE verifier the caller needs to complete Exchange.
func AuthCodeFlow(ctx context.Context, m Metadata, clientID, scope string, open func(url string) error) (code, redirectURI, verifier string, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", "", err
	}
	defer ln.Close()
	redirectURI = "http://" + ln.Addr().String() + "/callback"

	verifier, challenge, err := PKCE()
	if err != nil {
		return "", "", "", err
	}
	state, err := randomState()
	if err != nil {
		return "", "", "", err
	}
	authURL := BuildAuthURL(m, clientID, redirectURI, challenge, state, scope)

	type result struct {
		code string
		err  error
	}
	resultCh := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("state") != state:
			http.Error(w, "state mismatch", http.StatusBadRequest)
			send(resultCh, result{err: errors.New("oauth: callback state mismatch")})
		case q.Get("error") != "":
			http.Error(w, "authorization failed: "+q.Get("error"), http.StatusBadRequest)
			send(resultCh, result{err: fmt.Errorf("oauth: authorization error: %s", q.Get("error"))})
		case q.Get("code") == "":
			http.Error(w, "missing code", http.StatusBadRequest)
			send(resultCh, result{err: errors.New("oauth: callback missing code")})
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<html><body>Signed in — you can close this tab and return to poddle.</body></html>")
			if f, ok := w.(http.Flusher); ok {
				f.Flush() // hand bytes to the kernel before we tear the listener down
			}
			send(resultCh, result{code: q.Get("code")})
		}
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	if err := open(authURL); err != nil {
		return "", redirectURI, verifier, fmt.Errorf("oauth: opening browser: %w", err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			return "", redirectURI, verifier, res.err
		}
		return res.code, redirectURI, verifier, nil
	case <-ctx.Done():
		return "", redirectURI, verifier, ctx.Err()
	}
}

// send is a non-blocking send: the channel is buffered 1 and only ever
// written once, but this keeps a stray second callback hit from hanging.
func send[T any](ch chan T, v T) {
	select {
	case ch <- v:
	default:
	}
}

func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
