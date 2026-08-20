package poddled

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/policy"
)

// roundTripFunc lets a test stand in for the daemon's HTTP surface without a
// real Unix socket, so these run on every platform (the real-daemon tests in
// client_test.go skip on Windows).
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// clientWithRT builds a Client whose transport is rt. It goes through NewClient
// so the socket-path wiring is exercised too, then swaps the transport.
func clientWithRT(rt roundTripFunc) *Client {
	c := NewClient("/tmp/poddled-unit-test.sock")
	c.http = &http.Client{Transport: rt}
	return c
}

func httpResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestClientUnit_RedisAddr(t *testing.T) {
	var gotPath string
	c := clientWithRT(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		return httpResp(200, `{"addr":"127.0.0.1:12345","redis":"127.0.0.1:16379","postgres":"127.0.0.1:15432"}`), nil
	})
	addr, err := c.RedisAddr()
	if err != nil || addr != "127.0.0.1:16379" {
		t.Fatalf("RedisAddr = %q, %v; want 127.0.0.1:16379, nil", addr, err)
	}
	if gotPath != "/gateway" {
		t.Errorf("path = %q, want /gateway", gotPath)
	}
}

func TestClientUnit_RedisAddr_NotReady(t *testing.T) {
	c := clientWithRT(func(*http.Request) (*http.Response, error) {
		return httpResp(200, `{"addr":"127.0.0.1:1","redis":"","postgres":"127.0.0.1:2"}`), nil
	})
	if _, err := c.RedisAddr(); err == nil {
		t.Error("expected error when the redis listener address is empty")
	}
}

func TestClientUnit_PostgresAddr(t *testing.T) {
	c := clientWithRT(func(*http.Request) (*http.Response, error) {
		return httpResp(200, `{"addr":"127.0.0.1:12345","redis":"127.0.0.1:16379","postgres":"127.0.0.1:15432"}`), nil
	})
	addr, err := c.PostgresAddr()
	if err != nil || addr != "127.0.0.1:15432" {
		t.Fatalf("PostgresAddr = %q, %v; want 127.0.0.1:15432, nil", addr, err)
	}
}

func TestClientUnit_PostgresAddr_NotReady(t *testing.T) {
	c := clientWithRT(func(*http.Request) (*http.Response, error) {
		return httpResp(200, `{"addr":"127.0.0.1:1","redis":"127.0.0.1:2","postgres":""}`), nil
	})
	if _, err := c.PostgresAddr(); err == nil {
		t.Error("expected error when the postgres listener address is empty")
	}
}

func TestClientUnit_Egress(t *testing.T) {
	var gotMethod, gotPath string
	c := clientWithRT(func(r *http.Request) (*http.Response, error) {
		gotMethod, gotPath = r.Method, r.URL.EscapedPath() // escaped, to verify path-escaping
		return httpResp(200, `{"token":"poddle_egr","addr":"127.0.0.1:9"}`), nil
	})
	tok, addr, err := c.Egress("my pod")
	if err != nil {
		t.Fatalf("Egress: %v", err)
	}
	if tok != "poddle_egr" || addr != "127.0.0.1:9" {
		t.Errorf("Egress = %q, %q; want poddle_egr, 127.0.0.1:9", tok, addr)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/pods/my%20pod/egress" { // pod name is path-escaped
		t.Errorf("path = %q, want /pods/my%%20pod/egress", gotPath)
	}
}

func TestClientUnit_SetPolicy(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	c := clientWithRT(func(r *http.Request) (*http.Response, error) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		return httpResp(http.StatusNoContent, ""), nil
	})
	if err := c.SetPolicy("box", &policy.Policy{Name: "guardrail"}); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/pods/box/policy" {
		t.Errorf("request = %s %s, want POST /pods/box/policy", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, "guardrail") {
		t.Errorf("policy body missing name: %q", gotBody)
	}
}

func TestClientUnit_SetPolicy_BadStatus(t *testing.T) {
	c := clientWithRT(func(*http.Request) (*http.Response, error) {
		return httpResp(http.StatusInternalServerError, ""), nil
	})
	if err := c.SetPolicy("box", &policy.Policy{Name: "x"}); err == nil {
		t.Error("expected error on non-204 status")
	}
}

func TestClientUnit_Audit(t *testing.T) {
	var gotPath string
	c := clientWithRT(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		return httpResp(http.StatusNoContent, ""), nil
	})
	if err := c.Audit(audit.Event{Kind: "pod.up", Pod: "box"}); err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if gotPath != "/audit" {
		t.Errorf("path = %q, want /audit", gotPath)
	}
}

func TestClientUnit_Audit_BadStatus(t *testing.T) {
	c := clientWithRT(func(*http.Request) (*http.Response, error) {
		return httpResp(http.StatusBadRequest, ""), nil
	})
	if err := c.Audit(audit.Event{}); err == nil {
		t.Error("expected error on non-204 status")
	}
}

func TestClientUnit_Audits_BuildsQuery(t *testing.T) {
	var gotQuery string
	c := clientWithRT(func(r *http.Request) (*http.Response, error) {
		gotQuery = r.URL.Query().Encode()
		return httpResp(200, `[]`), nil
	})
	events, err := c.Audits(audit.Filter{Pod: "box", Kind: "egress", Decision: "deny", SinceSeq: 7, Limit: 25})
	if err != nil {
		t.Fatalf("Audits: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %v, want empty", events)
	}
	for _, want := range []string{"pod=box", "kind=egress", "decision=deny", "since=7", "limit=25"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func TestClientUnit_VerifyAudit(t *testing.T) {
	c := clientWithRT(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/audit/verify" {
			t.Errorf("path = %q, want /audit/verify", r.URL.Path)
		}
		return httpResp(200, `{"ok":false,"brokenAt":42}`), nil
	})
	ok, brokenAt, err := c.VerifyAudit()
	if err != nil {
		t.Fatalf("VerifyAudit: %v", err)
	}
	if ok || brokenAt != 42 {
		t.Errorf("VerifyAudit = %v, %d; want false, 42", ok, brokenAt)
	}
}

func TestClientUnit_TransportError(t *testing.T) {
	c := clientWithRT(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})
	if _, err := c.RedisAddr(); err == nil {
		t.Error("expected the transport error to propagate")
	}
}
