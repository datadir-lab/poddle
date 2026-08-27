package poddled

import (
	"io"
	"net/http"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/policy"
)

// Every Client method must surface a transport error rather than swallow it.
func TestClientUnit_AllMethods_TransportError(t *testing.T) {
	c := clientWithRT(func(*http.Request) (*http.Response, error) { return nil, io.ErrClosedPipe })

	checks := []struct {
		name string
		call func() error
	}{
		{"Health", c.Health},
		{"Gateway", func() error { _, err := c.Gateway(); return err }},
		{"RedisAddr", func() error { _, err := c.RedisAddr(); return err }},
		{"PostgresAddr", func() error { _, err := c.PostgresAddr(); return err }},
		{"IssueHandle", func() error { _, err := c.IssueHandle("p", "s", broker.Credential{}); return err }},
		{"Status", func() error { _, err := c.Status(); return err }},
		{"Egress", func() error { _, _, err := c.Egress("p"); return err }},
		{"SetPolicy", func() error { return c.SetPolicy("p", &policy.Policy{}) }},
		{"Audit", func() error { return c.Audit(audit.Event{}) }},
		{"Audits", func() error { _, err := c.Audits(audit.Filter{}); return err }},
		{"VerifyAudit", func() error { _, _, err := c.VerifyAudit(); return err }},
		{"RevokePod", func() error { return c.RevokePod("p") }},
		{"OAuthMirror", func() error { _, err := c.OAuthMirror(); return err }},
	}
	for _, ch := range checks {
		if err := ch.call(); err == nil {
			t.Errorf("%s: expected the transport error to propagate", ch.name)
		}
	}
}

// A 200 with an unparseable body must be a decode error, not a silent zero value.
func TestClientUnit_DecodeErrors(t *testing.T) {
	c := clientWithRT(func(*http.Request) (*http.Response, error) { return httpResp(200, "{not json"), nil })

	if _, err := c.Gateway(); err == nil {
		t.Error("Gateway: expected a decode error")
	}
	if _, err := c.Status(); err == nil {
		t.Error("Status: expected a decode error")
	}
	if _, _, err := c.Egress("p"); err == nil {
		t.Error("Egress: expected a decode error")
	}
	if _, err := c.Audits(audit.Filter{}); err == nil {
		t.Error("Audits: expected a decode error")
	}
	if _, _, err := c.VerifyAudit(); err == nil {
		t.Error("VerifyAudit: expected a decode error")
	}
	if _, err := c.OAuthMirror(); err == nil {
		t.Error("OAuthMirror: expected a decode error")
	}
}

// Non-success statuses must become errors on the methods that check them.
func TestClientUnit_BadStatusErrors(t *testing.T) {
	c := clientWithRT(func(*http.Request) (*http.Response, error) { return httpResp(http.StatusInternalServerError, ""), nil })

	if err := c.Health(); err == nil {
		t.Error("Health: expected an error on 500")
	}
	if _, err := c.IssueHandle("p", "s", broker.Credential{}); err == nil {
		t.Error("IssueHandle: expected an error on 500")
	}
	if err := c.RevokePod("p"); err == nil {
		t.Error("RevokePod: expected an error on 500")
	}
	if _, err := c.OAuthMirror(); err == nil {
		t.Error("OAuthMirror: expected an error on 500")
	}
}
