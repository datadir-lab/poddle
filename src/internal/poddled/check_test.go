package poddled

import (
	"testing"

	"github.com/datadir-lab/poddle/src/internal/policy"
)

// A policy pod must not escape its own allow-list by presenting an empty or
// bogus egress token: an unrecognized token maps to no pod, so Check denies it
// rather than falling through to a nil (default-allow) policy.
func TestDaemon_Check_DeniesUnknownOrEmptyToken(t *testing.T) {
	d := New(&fakeBroker{}, nil)
	d.handlePod["tok-box"] = "box"
	d.podPolicy["box"] = &policy.Policy{Name: "locked", AllowUpstreams: []string{"allowed.test"}}

	if ok, _ := d.Check("tok-box", "allowed.test", "GET"); !ok {
		t.Error("valid token to an allow-listed host must be allowed")
	}
	if ok, _ := d.Check("tok-box", "evil.test", "GET"); ok {
		t.Error("valid token to a non-allow-listed host must be denied by the policy")
	}
	for _, tok := range []string{"", "poddle_egr_bogus"} {
		if ok, reason := d.Check(tok, "evil.test", "GET"); ok {
			t.Errorf("token %q must be denied (got allow: %q) — a stripped token must not bypass the allow-list", tok, reason)
		}
	}
}

// A known pod with no policy bound is default-allow: its egress is
// non-bypassable (through the broker, audited) but unrestricted until a policy
// is attached — Check must not deny it merely for lacking a policy.
func TestDaemon_Check_KnownPodNoPolicyDefaultsAllow(t *testing.T) {
	d := New(&fakeBroker{}, nil)
	d.handlePod["tok"] = "box" // known pod, no podPolicy entry
	if ok, _ := d.Check("tok", "anywhere.test", "GET"); !ok {
		t.Error("a known pod with no policy must default-allow")
	}
}
