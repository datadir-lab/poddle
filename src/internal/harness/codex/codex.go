// Package codex implements the harness.Harness for OpenAI's Codex CLI in a pod.
// Codex 0.149.1 ignores OPENAI_BASE_URL, so the harness redirects it to the broker
// via model_providers `-c` overrides passed on every `codex exec` invocation (see
// codexProviderFlags) and presents a handle via OPENAI_API_KEY (which Codex sends
// as Authorization: Bearer) — so the real OpenAI secret never enters the pod, and
// codex never writes (or clobbers) the user's own $CODEX_HOME/config.toml.
// Verified against real Codex 0.149.1 in the Task 1 spike.
package codex

import "strings"

type Harness struct{}

func New() *Harness { return &Harness{} }

func (h *Harness) Name() string { return "codex" }

// Provisions installs the Codex CLI (npm — the same node image as claude-code).
// The broker provider is no longer written to config.toml here — it rides -c
// overrides (codexProviderFlags) on every `codex exec` instead, so codex never
// touches the user's own $CODEX_HOME/config.toml (e.g. their [mcp_servers]).
func (h *Harness) Provisions() []string {
	return []string{"npm install -g @openai/codex"}
}

func (h *Harness) Supports(vendor string) bool { return vendor == "openai" }

// Env points Codex at the broker. OPENAI_API_KEY carries the handle (sent as
// Bearer); PODDLE_CODEX_BASE_URL carries the broker's /v1 base (read by the
// codexProviderFlags -c overrides on `codex exec`, since Codex ignores
// OPENAI_BASE_URL); CODEX_HOME fixes the config + state location (matches
// StateDirs). BaseURL rides /v1 in the request path — the credential's upstream
// has no path, so no /v1/v1.
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

// codexProviderFlags inject the broker provider via -c overrides so codex never
// writes (or clobbers) the user's ~/.codex/config.toml. String values are quoted
// TOML; base_url expands $PODDLE_CODEX_BASE_URL (set by Env) at run time in the
// pod shell. Verified against a real codex arg parse.
const codexProviderFlags = `-c 'model_provider="poddle"' ` +
	`-c 'model_providers.poddle.name="poddle"' ` +
	`-c model_providers.poddle.base_url="\"$PODDLE_CODEX_BASE_URL\"" ` +
	`-c 'model_providers.poddle.env_key="OPENAI_API_KEY"' ` +
	`-c 'model_providers.poddle.wire_api="responses"'`

// TaskCommand runs Codex headless (one-shot) to completion. Codex bounds its own
// turns; maxTurns is advisory here (documented; a flag mapping can follow).
func (h *Harness) TaskCommand(prompt string, _ int) string {
	return "codex exec " + execFlags + " " + codexProviderFlags + " " + shellSingleQuote(prompt)
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
	return "codex exec resume --last " + execFlags + " " + codexProviderFlags + " " + shellSingleQuote(resumeNudge)
}

// EgressHosts is what Codex needs to install and run: the npm registry and
// OpenAI's API host. Seeds the default-deny allow-list.
func (h *Harness) EgressHosts() []string {
	return []string{"registry.npmjs.org", "api.openai.com"}
}

// ConfigDir is CODEX_HOME (/root/.codex), where Codex reads config.toml and
// other user-customizable settings. Seeded from the user's host config and
// persisted as a named volume so customizations survive `move`.
func (h *Harness) ConfigDir() string { return "/root/.codex" }

// MCPWiring registers a brokered MCP server with Codex via `codex mcp add` — its
// native command, which merges an [mcp_servers.<name>] entry into config.toml
// (preserving the rest) and applies to every codex invocation. bearer-token-env-var
// makes Codex read the handle from the env var at runtime, so it never lands on
// disk. Runs after Provisions installs codex. Verified against Codex 0.149.1.
func (h *Harness) MCPWiring(name, agentURL, handleEnv string) []string {
	return []string{"codex mcp add " + shellSingleQuote(name) +
		" --url " + shellSingleQuote(agentURL) +
		" --bearer-token-env-var " + shellSingleQuote(handleEnv)}
}
