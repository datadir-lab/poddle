package poddled

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/policy"
)

func TestDaemon_Status(t *testing.T) {
	srv, _ := testServer(t)
	issue(t, srv.URL, "box")

	resp, err := http.Get(srv.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s Status
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Gateway != "0.0.0.0:9999" {
		t.Errorf("gateway = %q, want 0.0.0.0:9999", s.Gateway)
	}
	if s.Pods["box"] != 1 {
		t.Errorf("pods = %v, want box:1", s.Pods)
	}
}

func TestDaemon_Egress(t *testing.T) {
	srv, _ := testServer(t)
	resp, err := http.Post(srv.URL+"/pods/box/egress", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var v map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v["token"], "poddle_egr_") {
		t.Errorf("token = %q, want a poddle_egr_ prefix", v["token"])
	}
}

func TestDaemon_Events_RecordedInStatus(t *testing.T) {
	srv, _ := testServer(t)
	// The host autoscaler POSTs its activity here; it must surface in /status.
	body := `{"msg":"autoscale: grew \"box\" weak->strong at 92% mem"}`
	resp, err := http.Post(srv.URL+"/events", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /events = %d, want 204", resp.StatusCode)
	}

	sr, err := http.Get(srv.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Body.Close()
	var s Status
	if err := json.NewDecoder(sr.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 1 || !strings.Contains(s.Events[0], "grew") {
		t.Errorf("status events = %v, want the pushed grow event", s.Events)
	}
}

func TestDaemon_SetPolicy(t *testing.T) {
	srv, _ := testServer(t)
	body, _ := json.Marshal(policy.Policy{Name: "guard"})
	resp, err := http.Post(srv.URL+"/pods/box/policy", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

func TestDaemon_PodPolicies(t *testing.T) {
	srv, _ := testServer(t)

	bind := func(pod, name string) {
		body, _ := json.Marshal(policy.Policy{Name: name})
		r, err := http.Post(srv.URL+"/pods/"+pod+"/policy", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
	}
	get := func() map[string]string {
		r, err := http.Get(srv.URL + "/pods/policies")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var m map[string]string
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	if got := get(); len(got) != 0 {
		t.Errorf("pod policies before any bind = %v, want empty", got)
	}
	bind("box", "guard")
	bind("box", "lockdown") // a rebind: the latest binding wins
	if got := get(); got["box"] != "lockdown" {
		t.Errorf("pod policies = %v, want box:lockdown (latest bind wins)", got)
	}
}

func TestDaemon_BadJSON_Returns400(t *testing.T) {
	srv, _ := testServer(t)
	for _, path := range []string{"/pods/box/policy", "/pods/box/handles", "/audit", "/events"} {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader("{not json"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %s with bad JSON = %d, want 400", path, resp.StatusCode)
		}
	}
}

func TestDaemon_Audit_NilStore(t *testing.T) {
	srv, _ := testServer(t) // testServer wires a nil audit store (auditing off)

	// POST /audit is a best-effort no-op that still returns 204.
	ev, _ := json.Marshal(audit.Event{Pod: "box", Kind: audit.KindPodUp})
	r1, err := http.Post(srv.URL+"/audit", "application/json", bytes.NewReader(ev))
	if err != nil {
		t.Fatal(err)
	}
	r1.Body.Close()
	if r1.StatusCode != http.StatusNoContent {
		t.Errorf("POST /audit = %d, want 204", r1.StatusCode)
	}

	// GET /audit returns an empty list, not an error.
	r2, err := http.Get(srv.URL + "/audit")
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Body.Close()
	var events []audit.Event
	if err := json.NewDecoder(r2.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("GET /audit (nil store) = %v, want empty", events)
	}

	// GET /audit/verify reports ok with no store to verify.
	r3, err := http.Get(srv.URL + "/audit/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Body.Close()
	var v struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(r3.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if !v.OK {
		t.Error("verify with nil store should report ok")
	}
}

func TestDaemon_AuditQueryFilters(t *testing.T) {
	store, err := audit.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := New(&fakeBroker{}, store)
	if _, err := d.Start("0.0.0.0:0", "redact", "", "", ""); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)

	// Exercises the since/limit parsing and the full Filter path.
	resp, err := http.Get(srv.URL + "/audit?pod=box&kind=request&decision=allow&since=1&limit=5")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var events []audit.Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestDaemon_Check(t *testing.T) {
	d := New(&fakeBroker{}, nil)
	// An unrecognized handle maps to no pod (hence no governing policy) and must
	// be DENIED — allowing it was a policy bypass (a pod could strip its egress
	// token to escape its own allow-list). A known pod with no policy still
	// default-allows; that path is covered in check_test.go.
	if allow, _ := d.Check("unknown-handle", "example.com", "GET"); allow {
		t.Error("Check with an unrecognized handle must deny, not allow")
	}
}

func TestDaemon_Monitored(t *testing.T) {
	d := New(&fakeBroker{}, nil)
	// An unknown handle (no policy) is not monitored.
	if d.Monitored("nope") {
		t.Error("an unknown handle must not be monitored")
	}
	d.handlePod["h1"] = "box"
	// A monitor-mode policy reports monitored; an enforce-mode one does not.
	d.podPolicy["box"] = &policy.Policy{Name: "watch", Monitor: true}
	if !d.Monitored("h1") {
		t.Error("a monitor-mode policy should report monitored")
	}
	d.podPolicy["box"] = &policy.Policy{Name: "strict"}
	if d.Monitored("h1") {
		t.Error("an enforce-mode policy must not report monitored")
	}
}

func TestDaemon_Intercepts(t *testing.T) {
	d := New(&fakeBroker{}, nil)
	if d.Intercepts("nope", "api.x.com") {
		t.Error("an unknown handle must not intercept")
	}
	d.handlePod["h1"] = "box"
	// Legacy bool: intercept all hosts.
	d.podPolicy["box"] = &policy.Policy{Name: "all", Intercept: true}
	if !d.Intercepts("h1", "anything.com") {
		t.Error("intercept:true should intercept every host")
	}
	// Per-host list: only matching hosts.
	d.podPolicy["box"] = &policy.Policy{Name: "scoped", InterceptHosts: []string{".x.com"}}
	if !d.Intercepts("h1", "api.x.com") {
		t.Error("a host under intercept_hosts should intercept")
	}
	if d.Intercepts("h1", "github.com") {
		t.Error("a host outside intercept_hosts must be tunnelled")
	}
}

func TestDaemon_EgressMode(t *testing.T) {
	d := New(&fakeBroker{}, nil)
	if got := d.EgressMode("nope"); got != "" {
		t.Errorf("unknown handle egress mode = %q, want empty", got)
	}
	d.handlePod["h1"] = "box"
	d.podPolicy["box"] = &policy.Policy{Name: "ro", Egress: "block"}
	if got := d.EgressMode("h1"); got != "block" {
		t.Errorf("egress mode = %q, want block", got)
	}
}

func TestDaemon_Resolve(t *testing.T) {
	d := New(&fakeBroker{}, nil)
	target, err := d.Resolve("some-handle")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target.Addr != "127.0.0.1:6379" || target.Pass != "realpass" {
		t.Errorf("target = %+v, want Addr 127.0.0.1:6379, Pass realpass", target)
	}
}
