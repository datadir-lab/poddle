// Package opencode implements the harness.Harness for the open-source `opencode`
// coding agent (opencode.ai / sst/opencode) in a pod, pointed at OpenAI-compatible
// models through the broker. opencode has no base-URL env var for arbitrary
// endpoints: a custom provider is configured via JSON (written in Provisions)
// using the @ai-sdk/openai-compatible provider, whose apiKey is
// "{env:OPENAI_API_KEY}" — opencode interpolates the pod's handle at runtime (never
// on disk) and sends it as Authorization: Bearer, so the broker swaps in the real
// secret. That provider config is written to a dedicated OPENCODE_CONFIG layer
// file (set by Env), not to the user's global ~/.config/opencode/opencode.json —
// opencode merges OPENCODE_CONFIG as an extra layer between global and project
// config, so the user's own global/project config is never overwritten and
// merges on top. Verified against opencode 1.18.23 in a spike; reuses the
// `openai` identity provider.
//
// opencode is heavier than the other harnesses: it always streams (stream:true),
// fires a title-generation call before the main turn, and on first run fetches
// ripgrep (GitHub) and a model catalog (models.dev) — hence the wider EgressHosts.
// A pre-baked opencode pod image (binary + rg + warm catalog) would shrink that
// footprint; that is a follow-up.
package opencode

import "strings"

type Harness struct{}

func New() *Harness { return &Harness{} }

func (h *Harness) Name() string { return "opencode" }

// Provisions installs opencode and writes poddle's provider config to the
// dedicated OPENCODE_CONFIG layer file (set by Env) — not to the user's global
// ~/.config/opencode/opencode.json, which is left untouched and merges on top.
// The config defines an OpenAI-compatible provider "poddle" whose baseURL is the
// broker (from $PODDLE_OPENCODE_BASE_URL, set by Env) and whose apiKey is
// "{env:OPENAI_API_KEY}" — opencode interpolates that env var (the pod's
// handle) at runtime, so the handle is never written to disk. The config-writer
// reads env vars Env() sets on the container (Setup inherits them).
func (h *Harness) Provisions() []string {
	return []string{
		"npm install -g opencode-ai@latest",
		`mkdir -p "$(dirname "$OPENCODE_CONFIG")" && printf '{"$schema":"https://opencode.ai/config.json","provider":{"poddle":{"npm":"@ai-sdk/openai-compatible","name":"Poddle","options":{"baseURL":"%s","apiKey":"{env:OPENAI_API_KEY}"},"models":{"poddle-model":{"name":"Poddle","limit":{"context":128000,"output":8192}}}}}}' "$PODDLE_OPENCODE_BASE_URL" > "$OPENCODE_CONFIG"`,
	}
}

func (h *Harness) Supports(vendor string) bool { return vendor == "openai" }

// Env points opencode at the broker. OPENAI_API_KEY carries the handle (referenced
// by the provider config's apiKey and sent as Bearer); PODDLE_OPENCODE_BASE_URL
// carries the broker's /v1 base (read by the Provisions config-writer — opencode
// has no base-URL env var). OPENCODE_CONFIG points opencode at a poddle-owned
// layer file (written by Provisions) instead of the user's global
// ~/.config/opencode/opencode.json, so the user's own global/project config is
// never overwritten and merges on top. The credential's upstream has no path, so
// opencode's /v1/chat/completions rides through once — no /v1/v1.
func (h *Harness) Env(brokerAddr, handle string) map[string]string {
	return map[string]string{
		"OPENAI_API_KEY":           handle,
		"PODDLE_OPENCODE_BASE_URL": strings.TrimRight(brokerAddr, "/") + "/v1",
		"OPENCODE_CONFIG":          "/run/poddle/opencode.json",
	}
}

// TaskCommand runs opencode headless (one prompt to completion). -m provider/model
// must match opencode.json; --format json gives a parseable event stream; --auto
// auto-approves tool permissions so an unattended run never blocks on a prompt (the
// pod is the sandbox).
func (h *Harness) TaskCommand(prompt string, _ int) string {
	return "opencode run " + shellSingleQuote(prompt) + " -m poddle/poddle-model --format json --auto"
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StateDirs is empty: opencode's config is rewritten by Provisions on each up/move,
// and session state is not persisted for a first cut.
func (h *Harness) StateDirs() []string { return nil }

// ResumeCommand is empty: opencode session resume (its -c/--continue) is not yet
// wired, so a `move` recreates the shell. (Follow-up.)
func (h *Harness) ResumeCommand(mode string) string { return "" }

// EgressHosts is what opencode needs to install and run: the npm registry
// (install), OpenAI's API host (LLM), GitHub (ripgrep binary auto-download), and
// models.dev (model catalog). Wider than the other harnesses because opencode
// fetches rg + the catalog on first run; all still flow through the governed,
// audited broker.
func (h *Harness) EgressHosts() []string {
	return []string{
		"registry.npmjs.org",
		"api.openai.com",
		"github.com",
		"objects.githubusercontent.com",
		"models.dev",
	}
}

// ConfigDir is opencode's global config directory (~/.config/opencode), where the
// user's own opencode.json and customizations live — untouched by poddle, whose
// provider config is written separately to the OPENCODE_CONFIG layer file.
// Seeded from the user's host config and persisted as a named volume so
// customizations survive `move`.
func (h *Harness) ConfigDir() string { return "/root/.config/opencode" }
