package broker

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

type hostAllow struct{ host string }

func (h hostAllow) Check(token, host, method string) (bool, string) {
	if host == h.host {
		return true, ""
	}
	return false, "destination not allow-listed"
}

func proxyClient(t *testing.T, proxyURL string) *http.Client {
	t.Helper()
	pu, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
}

func TestForwardProxy_ForwardsAllowedDestination(t *testing.T) {
	up, rec := upstreamRecording(t)
	upURL, _ := url.Parse(up.URL)

	fp := httptest.NewServer(NewForwardProxy(hostAllow{host: upURL.Hostname()}, nil))
	t.Cleanup(fp.Close)

	resp, err := proxyClient(t, fp.URL).Get(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || rec.method != http.MethodGet {
		t.Fatalf("an allowed destination should be forwarded to the upstream; status=%d method=%q",
			resp.StatusCode, rec.method)
	}
}

func TestForwardProxy_BlocksDeniedDestination(t *testing.T) {
	up, rec := upstreamRecording(t)

	fp := httptest.NewServer(NewForwardProxy(hostAllow{host: "never.example.invalid"}, nil))
	t.Cleanup(fp.Close)

	resp, err := proxyClient(t, fp.URL).Get(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a denied destination should 403, got %d", resp.StatusCode)
	}
	if rec.method != "" {
		t.Error("a policy-denied egress must never reach the upstream")
	}
}
