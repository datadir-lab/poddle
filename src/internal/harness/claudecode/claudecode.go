// Package claudecode implements the harness.Harness for Claude Code running
// inside a pod. It points claude-code at the broker via ANTHROPIC_BASE_URL and
// presents a handle via ANTHROPIC_AUTH_TOKEN (which claude-code sends as
// Authorization: Bearer) — so the real Anthropic credential never enters the
// pod. The ANTHROPIC_AUTH_TOKEN → Bearer behaviour is verified against real
// Claude Code at task 1.14.
package claudecode

// Harness is the Claude Code pod-side runtime.
type Harness struct{}

// New returns a Claude Code harness.
func New() *Harness { return &Harness{} }

func (h *Harness) Name() string { return "claude-code" }

// Provisions installs the Claude Code CLI. It assumes npm is present in the pod
// image; wiring a node-capable image/template is handled at 1.11/1.14.
func (h *Harness) Provisions() []string {
	return []string{"npm i -g @anthropic-ai/claude-code"}
}

func (h *Harness) Supports(vendor string) bool { return vendor == "anthropic" }

func (h *Harness) Env(brokerAddr, handle string) map[string]string {
	return map[string]string{
		"ANTHROPIC_BASE_URL":   brokerAddr,
		"ANTHROPIC_AUTH_TOKEN": handle,
	}
}
