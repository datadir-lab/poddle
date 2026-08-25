// Package claudecode implements the harness.Harness for Claude Code running
// inside a pod. It points claude-code at the broker via ANTHROPIC_BASE_URL and
// presents a handle via ANTHROPIC_AUTH_TOKEN (which claude-code sends as
// Authorization: Bearer) — so the real Anthropic credential never enters the
// pod. The ANTHROPIC_AUTH_TOKEN → Bearer behaviour is verified against real
// Claude Code at task 1.14.
package claudecode

import (
	"fmt"
	"strings"
)

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

// onboardingMerge marks Claude Code's onboarding complete WITHOUT overwriting the
// user's ~/.claude.json (where user-scope MCP servers and settings live). It reads
// the existing file if present, sets hasCompletedOnboarding=true, and writes it back
// — a merge, not a clobber. node is always present (claude-code runs on it). Verified
// against node: preserves mcpServers/theme; writes {"hasCompletedOnboarding":true} when absent.
const onboardingMerge = `node -e 'const f=process.env.HOME+"/.claude.json",fs=require("fs");let c={};try{c=JSON.parse(fs.readFileSync(f,"utf8"))}catch(e){};c.hasCompletedOnboarding=true;fs.writeFileSync(f,JSON.stringify(c))'`

// TaskCommand runs claude-code headless to completion. It uses the verified
// headless recipe: IS_SANDBOX + disabled non-essential traffic, a pre-seeded
// onboarding marker, and `-p` with stdin from /dev/null (which otherwise blocks).
func (h *Harness) TaskCommand(prompt string, maxTurns int) string {
	if maxTurns < 1 {
		maxTurns = 24
	}
	return "export IS_SANDBOX=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1; " +
		onboardingMerge + "; " +
		fmt.Sprintf("claude -p %s --output-format json --max-turns %d --dangerously-skip-permissions </dev/null",
			shellSingleQuote(prompt), maxTurns)
}

// shellSingleQuote wraps s in single quotes, safe for arbitrary prompt text.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StateDirs persists Claude Code's state (conversation history, projects) so a
// moved pod resumes where it left off. The pod runs as root, so this is
// /root/.claude.
func (h *Harness) StateDirs() []string { return []string{"/root/.claude"} }

// EgressHosts is what Claude Code needs to install and run: its npm registry
// and Anthropic's endpoints (API, telemetry). The pod's identity already allows
// its own API host; these seed the default-deny allow-list so `npm i` and the
// agent's own traffic work while exfiltration elsewhere stays blocked.
func (h *Harness) EgressHosts() []string {
	return []string{"registry.npmjs.org", ".anthropic.com"}
}

// resumeNudge is the prompt fed to a headless resume. `claude -p` needs a turn
// to drive; on a move the agent should pick its interrupted work back up, so we
// hand it a continuation nudge rather than an empty stdin (which would no-op).
const resumeNudge = "continue where you left off"

// ResumeCommand continues the most recent conversation (carried over in
// /root/.claude) after a move. Interactive re-opens the TTY session; headless
// resumes non-interactively, nudged to carry on to completion.
func (h *Harness) ResumeCommand(mode string) string {
	if mode == "interactive" {
		return "claude --continue"
	}
	return "export IS_SANDBOX=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1; " +
		onboardingMerge + "; " +
		fmt.Sprintf("claude -p %s --continue --output-format json --dangerously-skip-permissions </dev/null",
			shellSingleQuote(resumeNudge))
}

// ConfigDir is empty: claude-code is env-provider (broker wiring goes through
// Env, not a seeded config file), so Provisions never clobbers a config file.
// The onboarding write, which used to overwrite ~/.claude.json outright, now
// merges into it (see onboardingMerge) so a user's existing mcpServers/settings
// survive. ~/.claude/ (conversation state, etc.) persists via StateDirs, and a
// project-scope .mcp.json persists via /workspace. A host-seed of the
// home-level ~/.claude.json itself (so it never needs a merge in the first
// place) is a rollout follow-up.
func (h *Harness) ConfigDir() string { return "" }

// MCPWiring is nil: claude-code's own non-clobbering MCP-registration channel
// (`claude mcp add`) is not yet wired here. Follow-up.
func (h *Harness) MCPWiring(_, _, _ string) []string { return nil }
