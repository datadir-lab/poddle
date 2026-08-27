// Package aider implements the harness.Harness for the aider coding agent in a
// pod, pointed at OpenAI models through the broker. aider (litellm under the hood)
// honors OPENAI_API_BASE + OPENAI_API_KEY directly — no config file — and sends the
// key as Authorization: Bearer, so the pod presents its handle in OPENAI_API_KEY and
// the broker swaps in the real secret. The real key never enters the pod. Verified
// against aider-chat 0.86.2 in a spike. Reuses the `openai` identity provider.
package aider

import "strings"

type Harness struct{}

func New() *Harness { return &Harness{} }

func (h *Harness) Name() string { return "aider" }

// Provisions installs aider. Assumes the pod image carries python 3.10-3.12 + pip
// + git (aider needs git; python 3.13 silently installs a broken aider — use a 3.12
// image). Mirrors claude-code assuming npm/node.
func (h *Harness) Provisions() []string {
	return []string{"pip install aider-chat"}
}

func (h *Harness) Supports(vendor string) bool { return vendor == "openai" }

// Env points aider at the broker. OPENAI_API_KEY carries the handle (sent as
// Bearer); OPENAI_API_BASE carries the broker's /v1 base (honored directly by
// aider/litellm; /v1 is required and not auto-added). The credential's upstream has
// no path, so the request path /v1/chat/completions rides through once.
func (h *Harness) Env(brokerAddr, handle string) map[string]string {
	return map[string]string{
		"OPENAI_API_KEY":  handle,
		"OPENAI_API_BASE": strings.TrimRight(brokerAddr, "/") + "/v1",
	}
}

// aiderFlags run aider non-interactively and quietly: one message then exit
// (--message + --yes-always), no streaming/pretty TTY output, and no update-check
// or analytics traffic (aider bundles analytics SDKs — keep an egress-locked pod's
// runtime traffic to the broker). --model picks the OpenAI model served via the broker.
const aiderFlags = "--yes-always --no-stream --no-pretty --no-check-update --no-analytics --model gpt-4o"

// TaskCommand runs aider headless (one message) to completion.
func (h *Harness) TaskCommand(prompt string, _ int) string {
	return "aider --message " + shellSingleQuote(prompt) + " " + aiderFlags
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StateDirs is empty: aider keeps its session state (chat history, caches) inside
// the git working tree, which travels with the pod's workdir — no separate volume.
func (h *Harness) StateDirs() []string { return nil }

const resumeNudge = "continue where you left off"

// ResumeCommand continues aider's most recent conversation from the workdir-local
// chat history. Interactive re-opens the session; headless drives it with a nudge.
func (h *Harness) ResumeCommand(mode string) string {
	if mode == "interactive" {
		return "aider --restore-chat-history"
	}
	return "aider --message " + shellSingleQuote(resumeNudge) + " --restore-chat-history " + aiderFlags
}

// EgressHosts is what aider needs to install and run: PyPI (pip install) and
// OpenAI's API host. Seeds the default-deny allow-list.
func (h *Harness) EgressHosts() []string {
	return []string{"pypi.org", "files.pythonhosted.org", "api.openai.com"}
}

// ConfigDir is empty by design, not by omission: aider's user config is a
// PROJECT file (.aider.conf.yml), read from $HOME/git-root/cwd — not a per-user
// dotfile poddle could seed into a fixed location. The pod's git workdir is
// /workspace, which is already a persisted volume, and poddle writes no aider
// config file at all (broker wiring is pure env: OPENAI_API_KEY/
// OPENAI_API_BASE), so there is nothing to seed and nothing poddle could
// clobber. TestE2E_HarnessConfig_AiderProjectFile (tests/e2e/harnessconfig_test.go)
// proves a user's /workspace/.aider.conf.yml is honored by aider in the pod.
func (h *Harness) ConfigDir() string { return "" }

// MCPWiring is nil: aider has no MCP auto-wiring implemented here yet.
func (h *Harness) MCPWiring(_, _, _ string) []string { return nil }
