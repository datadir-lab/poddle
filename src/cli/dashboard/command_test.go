package dashboard

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/policy"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

func TestHandler_ServesEmbeddedBundle(t *testing.T) {
	srv := httptest.NewServer(Handler(filepath.Join(t.TempDir(), "absent.sock"), nil, nil))
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
	// The page loads the built SPA bundle (which speaks the /v1 contract).
	if !strings.Contains(string(body), "assets/") {
		t.Errorf("the page should load the built app bundle:\n%s", body)
	}
}

func TestHandler_SPAFallback(t *testing.T) {
	srv := httptest.NewServer(Handler(filepath.Join(t.TempDir(), "absent.sock"), nil, nil))
	t.Cleanup(srv.Close)

	// A client-side route path must serve the SPA shell (index.html) so deep
	// links and refreshes work with history-API routing.
	for _, path := range []string{"/pods", "/audit", "/pods/my-agent", "/policies/prod"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "assets/") {
			t.Fatalf("GET %s should serve the SPA shell; got %d:\n%s", path, resp.StatusCode, body)
		}
	}

	// A missing asset must still 404 — the fallback must not mask it (else a
	// stale hashed bundle reference would silently serve HTML).
	r2, err := http.Get(srv.URL + "/assets/does-not-exist.js")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset should 404, got %d", r2.StatusCode)
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

	srv := httptest.NewServer(Handler(sock, nil, nil))
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
	srv := httptest.NewServer(Handler(filepath.Join(t.TempDir(), "absent.sock"), store, nil))
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

func TestHandler_PodsAPI(t *testing.T) {
	pods := func() ([]sandbox.PodView, error) {
		return []sandbox.PodView{
			{Name: "agent1", State: "running", Size: "weak", Mode: "headless", Policy: "prod", Autoscale: true, CPU: "12.5%", MemPerc: "68%", Mem: "2.7GB / 4GB"},
		}, nil
	}
	srv := httptest.NewServer(Handler(filepath.Join(t.TempDir(), "absent.sock"), nil, pods))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/v1/pods")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{`"name":"agent1"`, `"state":"running"`, `"policy":"prod"`, `"cpu":"12.5%"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/v1/pods should include %s; got:\n%s", want, body)
		}
	}
}
