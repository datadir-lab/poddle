package dashboard

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/policy"
)

func TestHandler_ServesEmbeddedBundle(t *testing.T) {
	srv := httptest.NewServer(Handler(filepath.Join(t.TempDir(), "absent.sock"), nil))
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

	srv := httptest.NewServer(Handler(sock, nil))
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

func TestHandler_PolicyCRUD(t *testing.T) {
	store := policy.NewFileStore(filepath.Join(t.TempDir(), "policies"))
	srv := httptest.NewServer(Handler(filepath.Join(t.TempDir(), "absent.sock"), store))
	t.Cleanup(srv.Close)

	// PUT creates a policy.
	body := `{"allow_upstreams":["api.anthropic.com"],"egress":"block"}`
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/policies/prod", strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT policy = %d", resp.StatusCode)
	}
	if p, err := store.Get("prod"); err != nil || p.Egress != "block" {
		t.Fatalf("PUT should persist the policy; got %+v err=%v", p, err)
	}

	// GET list shows it.
	list, _ := http.Get(srv.URL + "/v1/policies")
	lb, _ := io.ReadAll(list.Body)
	list.Body.Close()
	if !strings.Contains(string(lb), "prod") || !strings.Contains(string(lb), "api.anthropic.com") {
		t.Errorf("GET /v1/policies should list prod:\n%s", lb)
	}

	// DELETE removes it.
	del, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/policies/prod", nil)
	dr, _ := http.DefaultClient.Do(del)
	dr.Body.Close()
	if names, _ := store.List(); len(names) != 0 {
		t.Errorf("DELETE should remove the policy; store still has %v", names)
	}
}
