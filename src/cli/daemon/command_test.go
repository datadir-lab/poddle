package daemon

import (
	"bytes"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/poddled"
)

// TestRenderStatus_NeedsReauth exercises `poddle daemon status`'s reauth
// block: a Status carrying flagged connection names must print each one with
// its `poddle connect reauth <name>` remediation hint.
func TestRenderStatus_NeedsReauth(t *testing.T) {
	var buf bytes.Buffer
	renderStatus(&buf, poddled.Status{
		Gateway:     "0.0.0.0:1234",
		Pods:        map[string]int{},
		NeedsReauth: []string{"gh"},
	})
	out := buf.String()
	if !strings.Contains(out, "needs reauth:") {
		t.Errorf("output missing the needs-reauth section:\n%s", out)
	}
	if !strings.Contains(out, "gh") {
		t.Errorf("output missing the flagged connection name %q:\n%s", "gh", out)
	}
	if !strings.Contains(out, "poddle connect reauth gh") {
		t.Errorf("output missing the remediation hint:\n%s", out)
	}
}

// TestRenderStatus_NoReauthNeeded confirms the section is omitted entirely
// when nothing is flagged, so `daemon status` output stays unchanged for the
// common case.
func TestRenderStatus_NoReauthNeeded(t *testing.T) {
	var buf bytes.Buffer
	renderStatus(&buf, poddled.Status{
		Gateway: "0.0.0.0:1234",
		Pods:    map[string]int{},
	})
	if strings.Contains(buf.String(), "needs reauth") {
		t.Errorf("output should omit the needs-reauth section when nothing is flagged:\n%s", buf.String())
	}
}
