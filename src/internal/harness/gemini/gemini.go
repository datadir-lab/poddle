// Package gemini implements the harness.Harness for Google's gemini-cli running
// inside a pod. It points gemini-cli at the broker via GOOGLE_GEMINI_BASE_URL and
// presents the pod's handle via GEMINI_API_KEY, sent as Authorization: Bearer
// (its GEMINI_API_KEY_AUTH_MECHANISM=bearer path) — so the real Google key never
// enters the pod; the broker swaps the handle for the real key as x-goog-api-key
// (broker.ModeGoogleAPIKey). Verified against gemini-cli 0.57.0 in a spike:
// docs/superpowers/specs/2026-08-26-gemini-harness-spike-findings.md.
package gemini

import (
	"fmt"
	"strings"
)

// Harness is the gemini-cli pod-side runtime.
type Harness struct{}

// New returns a gemini-cli harness.
func New() *Harness { return &Harness{} }

func (h *Harness) Name() string { return "gemini" }

// authSettingsMerge marks the API-key auth path as selected in
// ~/.gemini/settings.json WITHOUT clobbering the user's other settings (theme,
// mcpServers, ...). Headless `gemini -p` otherwise exits 41 "Invalid auth method
// selected" — the env var GEMINI_DEFAULT_AUTH_TYPE alone is NOT honored; only the
// nested settings key security.auth.selectedType is (spike-confirmed on 0.57.0).
// node is present (gemini-cli runs on it). A merge, not a write, so a seeded/
// persisted user settings.json survives.
const authSettingsMerge = `mkdir -p "$HOME/.gemini" && node -e 'const f=process.env.HOME+"/.gemini/settings.json",fs=require("fs");let c={};try{c=JSON.parse(fs.readFileSync(f,"utf8"))}catch(e){};(c.security=c.security||{}).auth=Object.assign(c.security.auth||{},{selectedType:"gemini-api-key"});fs.writeFileSync(f,JSON.stringify(c))'`

// Provisions installs gemini-cli and selects the API-key auth path. --ignore-scripts
// keeps the npm install side-effect-free; the settings merge runs after (node from
// the same install). Assumes a node-capable image (Node >=20).
func (h *Harness) Provisions() []string {
	return []string{
		"npm install -g --ignore-scripts @google/gemini-cli",
		authSettingsMerge,
	}
}

func (h *Harness) Supports(vendor string) bool { return vendor == "google" }

// Env points gemini-cli at the broker, secretlessly.
//   - GEMINI_API_KEY carries the pod's handle (the "key" gemini-cli sends).
//   - GEMINI_API_KEY_AUTH_MECHANISM=bearer makes it ALSO send Authorization:
//     Bearer <handle>, which the broker's handleFromAuth reads.
//   - GOOGLE_GEMINI_BASE_URL is the broker ORIGIN (no path); the @google/genai SDK
//     appends /v1beta/models/... itself, and that path rides through to upstream.
//   - GEMINI_CLI_TRUST_WORKSPACE=true is REQUIRED, else gemini-cli refuses to run
//     in the pod workdir ("not a trusted directory") and --yolo is downgraded.
//   - GEMINI_SANDBOX=false because the pod is already the sandbox.
//
// The model is deliberately NOT pinned here: gemini-cli falls back to its own
// default, and leaving GEMINI_MODEL unset means a user's template `[env]
// GEMINI_MODEL = "gemini-2.5-pro"` survives (harness Env is layered over template
// env, so a value set here would clobber the user's choice).
func (h *Harness) Env(brokerAddr, handle string) map[string]string {
	return map[string]string{
		"GEMINI_API_KEY":                handle,
		"GEMINI_API_KEY_AUTH_MECHANISM": "bearer",
		"GOOGLE_GEMINI_BASE_URL":        strings.TrimRight(brokerAddr, "/"),
		"GEMINI_CLI_TRUST_WORKSPACE":    "true",
		"GEMINI_SANDBOX":                "false",
	}
}

// TaskCommand runs gemini-cli headless to completion. `-p` with stdin from
// /dev/null (which otherwise blocks — headless-hang #12362); --output-format json
// prints one JSON object; --yolo auto-approves tool calls (effective only in the
// trusted workspace set up by Env). gemini-cli has no max-turns knob, so maxTurns
// is unused.
func (h *Harness) TaskCommand(prompt string, _ int) string {
	return fmt.Sprintf("gemini -p %s --output-format json --yolo </dev/null", shellSingleQuote(prompt))
}

// shellSingleQuote wraps s in single quotes, safe for arbitrary prompt text.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StateDirs is empty: gemini-cli's session state lives under ConfigDir
// (~/.gemini), which is already persisted as a named volume — a separate state
// volume would collide with it.
func (h *Harness) StateDirs() []string { return nil }

// ResumeCommand is empty: gemini-cli session resume is not yet wired, so a `move`
// recreates the shell rather than continuing the conversation. (Follow-up.)
func (h *Harness) ResumeCommand(mode string) string { return "" }

// EgressHosts is what gemini-cli needs to install and run: the npm registry and
// Google's generative-language API host. Seeds the default-deny allow-list.
func (h *Harness) EgressHosts() []string {
	return []string{"registry.npmjs.org", "generativelanguage.googleapis.com"}
}

// ConfigDir is ~/.gemini, where gemini-cli keeps user config (settings.json,
// mcpServers) and session state. poddle seeds a user's host config here and
// persists it as a named volume; the auth-path selection Provisions writes is
// merged into it (see authSettingsMerge), not clobbered.
func (h *Harness) ConfigDir() string { return "/root/.gemini" }

// MCPWiring returns nil: brokered-MCP wiring for gemini-cli is a deferred
// follow-up. Unlike aider (which has no MCP client at all), gemini-cli DOES
// support MCP servers via settings.json `mcpServers` — wiring it is future work,
// not a permanent gap.
func (h *Harness) MCPWiring(name, agentURL, handleEnv string) []string { return nil }
