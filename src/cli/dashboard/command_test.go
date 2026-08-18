package dashboard

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandler_ServesEmbeddedBundle(t *testing.T) {
	srv := httptest.NewServer(Handler(filepath.Join(t.TempDir(), "absent.sock")))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "poddle") {
		t.Fatalf("expected the embedded dashboard page, got %d:\n%s", resp.StatusCode, body)
	}
	// The page speaks the versioned contract, so the same bundle works for cloud.
	if !strings.Contains(string(body), "/v1") {
		t.Error("the page should call the /v1 audit contract")
	}
}

func TestHandler_ProxiesV1AuditToDaemon(t *testing.T) {
	// A stub daemon on a Unix socket answering the /audit* routes.
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/audit" {
			_, _ = w.Write([]byte(`[{"seq":1,"kind":"request","pod":"proj"}]`))
			return
		}
		http.NotFound(w, r)
	}))

	srv := httptest.NewServer(Handler(sock))
	defer srv.Close()

	// /v1/audit must map to the daemon's /audit and preserve the query.
	resp, err := http.Get(srv.URL + "/v1/audit?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"kind":"request"`) {
		t.Fatalf("/v1/audit should proxy to the daemon's /audit; got:\n%s", body)
	}
}
