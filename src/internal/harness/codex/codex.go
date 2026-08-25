// Package codex implements the harness.Harness for OpenAI's Codex CLI in a pod.
// Codex 0.149.1 ignores OPENAI_BASE_URL, so the harness redirects it to the broker
// with a $CODEX_HOME/config.toml custom provider (written in Provisions) and
// presents a handle via OPENAI_API_KEY (which Codex sends as Authorization:
// Bearer) — so the real OpenAI secret never enters the pod. Verified against real
// Codex 0.149.1 in the Task 1 spike.
package codex

import "strings"

type Harness struct{}

func New() *Harness { return &Harness{} }

func (h *Harness) Name() string { return "codex" }

// Provisions installs the Codex CLI (npm — the same node image as claude-code)
// and writes the config.toml that redirects Codex to the broker. The config-writer
// reads CODEX_HOME + PODDLE_CODEX_BASE_URL, which Env() sets on the container, so
// they are available to this Setup step (podman exec inherits the container env).
func (h *Harness) Provisions() []string {
	return []string{
		"npm install -g @openai/codex",
		`mkdir -p "$CODEX_HOME" && printf 'model_provider = "poddle"\n\n[model_providers.poddle]\nname = "poddle"\nbase_url = "%s"\nenv_key = "OPENAI_API_KEY"\nwire_api = "responses"\n' "$PODDLE_CODEX_BASE_URL" > "$CODEX_HOME/config.toml"`,
	}
}

func (h *Harness) Supports(vendor string) bool { return vendor == "openai" }

// Env points Codex at the broker. OPENAI_API_KEY carries the handle (sent as
// Bearer); PODDLE_CODEX_BASE_URL carries the broker's /v1 base (read by the
// Provisions config-writer, since Codex ignores OPENAI_BASE_URL); CODEX_HOME fixes
// the config + state location (matches StateDirs). BaseURL rides /v1 in the
// request path — the credential's upstream has no path, so no /v1/v1.
func (h *Harness) Env(brokerAddr, handle string) map[string]string {
	return map[string]string{
		"OPENAI_API_KEY":        handle,
		"CODEX_HOME":            "/root/.codex",
		"PODDLE_CODEX_BASE_URL": strings.TrimRight(brokerAddr, "/") + "/v1",
	}
}

// execFlags run Codex non-interactively with write access. The pod is already an
// isolated, secretless, egress-locked sandbox — the same premise on which
// claude-code runs with --dangerously-skip-permissions — so Codex's own default
// read-only sandbox and approval prompts are both redundant and blocking for an
// autonomous task. --dangerously-bypass-approvals-and-sandbox (whose own help
// says it is "intended solely for running in environments that are externally
// sandboxed") lets `poddle task` actually apply edits; --skip-git-repo-check
// allows a non-git workdir.
const execFlags = "--dangerously-bypass-approvals-and-sandbox --skip-git-repo-check"

// TaskCommand runs Codex headless (one-shot) to completion. Codex bounds its own
// turns; maxTurns is advisory here (documented; a flag mapping can follow).
func (h *Harness) TaskCommand(prompt string, _ int) string {
	return "codex exec " + execFlags + " " + shellSingleQuote(prompt)
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StateDirs persists Codex state (CODEX_HOME = /root/.codex: config, auth, sessions)
// so a moved pod resumes its session.
func (h *Harness) StateDirs() []string { return []string{"/root/.codex"} }

const resumeNudge = "continue where you left off"

// ResumeCommand continues the most recent Codex session after a move. Headless
// resume runs autonomously (execFlags, like TaskCommand); interactive hands the
// TTY back so the user drives and approves.
func (h *Harness) ResumeCommand(mode string) string {
	if mode == "interactive" {
		return "codex resume --last"
	}
	return "codex exec resume --last " + execFlags + " " + shellSingleQuote(resumeNudge)
}

// EgressHosts is what Codex needs to install and run: the npm registry and
// OpenAI's API host. Seeds the default-deny allow-list.
func (h *Harness) EgressHosts() []string {
	return []string{"registry.npmjs.org", "api.openai.com"}
}
