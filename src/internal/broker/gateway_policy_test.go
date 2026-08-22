package broker

import (
	"net/http"
	"strings"
	"testing"
)

type denyChecker struct{ reason string }

func (d denyChecker) Check(handle, host, method string) (bool, string) { return false, d.reason }

type readOnlyChecker struct{}

func (readOnlyChecker) Check(handle, host, method string) (bool, string) {
	if method == http.MethodGet {
		return true, ""
	}
	return false, "read-only policy"
}

func TestGateway_PolicyDenies_NeverReachesUpstream(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "s", BaseURL: up.URL})
	g.SetPolicyChecker(denyChecker{reason: "not allow-listed"})
	srv := serve(t, g)

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+handle)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a policy-denied request must 403, got %d", resp.StatusCode)
	}
	if rec.method != "" {
		t.Error("a policy-denied request must never reach the upstream")
	}
}

// monitorChecker denies every request via Check but reports monitor mode, so the
// gateway should forward the request and record it as "monitor".
type monitorChecker struct{}

func (monitorChecker) Check(handle, host, method string) (bool, string) {
	return false, "not allow-listed"
}
func (monitorChecker) Monitored(handle string) bool { return true }

func TestGateway_MonitorMode_ForwardsAndRecordsWouldDeny(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "s", BaseURL: up.URL})
	aud := &recAuditor{}
	g.SetAuditor(aud)
	g.SetPolicyChecker(monitorChecker{})
	srv := serve(t, g)

	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+handle)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Monitor mode forwards the would-be denial instead of blocking it.
	if resp.StatusCode != http.StatusOK || rec.method != http.MethodPost {
		t.Fatalf("monitor mode should forward the request; status=%d upstream-method=%q", resp.StatusCode, rec.method)
	}
	// ...and records the outcome as "monitor" (not "allow" or "deny").
	recs := aud.all()
	if len(recs) != 1 || recs[0].Decision != "monitor" {
		t.Fatalf("expected one 'monitor' audit record, got %+v", recs)
	}
	if !strings.Contains(recs[0].Detail, "would deny") {
		t.Errorf("monitor record should note the would-be denial; detail=%q", recs[0].Detail)
	}
}

func TestGateway_PolicyAllowsPermittedMethod(t *testing.T) {
	up, rec := upstreamRecording(t)
	g, handle := gatewayWith(t, Credential{Mode: ModeSubscription, Secret: "s", BaseURL: up.URL})
	g.SetPolicyChecker(readOnlyChecker{})
	srv := serve(t, g)

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+handle)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || rec.method != http.MethodGet {
		t.Fatalf("a permitted GET should reach the upstream; status=%d upstream-method=%q", resp.StatusCode, rec.method)
	}
}
