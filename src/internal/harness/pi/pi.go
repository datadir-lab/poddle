// Package pi implements the harness.Harness for the open-source `pi` coding agent
// (@earendil-works/pi-coding-agent) in a pod, pointed at OpenAI-compatible models
// through the broker. pi has no generic base-URL env var: a custom endpoint is
// configured via a models.json file (relocatable with PI_CODING_AGENT_DIR), which
// the harness writes in Provisions. pi sends the configured apiKey as
// Authorization: Bearer, so the pod presents its handle (via $OPENAI_API_KEY,
// interpolated into models.json) and the broker swaps in the real secret — the
// real key never enters the pod. Verified against pi 0.84.3 in a spike; reuses the
// `openai` identity provider.
package pi

import "strings"

type Harness struct{}

func New() *Harness { return &Harness{} }

func (h *Harness) Name() string { return "pi" }

// Provisions installs pi and writes the models.json that points it at the broker.
// The config defines one OpenAI-compatible provider "poddle" whose baseUrl is the
// broker (read from $PODDLE_PI_BASE_URL, set by Env) and whose apiKey is
// "$OPENAI_API_KEY" — pi interpolates that env var (the pod's handle) at runtime,
// so the handle is never written to disk. --ignore-scripts keeps the npm install
// side-effect-free. The config-writer reads env vars Env() sets on the container,
// which the Setup step (podman exec) inherits.
func (h *Harness) Provisions() []string {
	return []string{
		"npm install -g --ignore-scripts @earendil-works/pi-coding-agent",
		`mkdir -p "$PI_CODING_AGENT_DIR" && printf '{"providers":{"poddle":{"baseUrl":"%s","api":"openai-completions","apiKey":"$OPENAI_API_KEY","authHeader":true,"models":[{"id":"poddle-model","name":"Poddle","reasoning":false,"input":["text"],"contextWindow":32000,"maxTokens":4096,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}' "$PODDLE_PI_BASE_URL" > "$PI_CODING_AGENT_DIR/models.json"`,
		"pi install npm:pi-mcp-adapter",
	}
}

func (h *Harness) Supports(vendor string) bool { return vendor == "openai" }

// Env points pi at the broker. OPENAI_API_KEY carries the handle (referenced by
// models.json's apiKey and sent as Bearer); PODDLE_PI_BASE_URL carries the broker's
// /v1 base (read by the Provisions config-writer — pi has no base-URL env var);
// PI_CODING_AGENT_DIR fixes the config location; PI_OFFLINE keeps pi from any
// non-broker network calls on a locked-down pod. The credential's upstream has no
// path, so pi's /v1/chat/completions rides through once — no /v1/v1.
func (h *Harness) Env(brokerAddr, handle string) map[string]string {
	return map[string]string{
		"OPENAI_API_KEY":      handle,
		"PI_CODING_AGENT_DIR": "/root/.pi",
		"PODDLE_PI_BASE_URL":  strings.TrimRight(brokerAddr, "/") + "/v1",
		"PI_OFFLINE":          "1",
		"PI_MCP_CONFIG_MODE":  "exclusive",
	}
}

// TaskCommand runs pi headless (one prompt to completion). --provider/--model must
// match models.json exactly (no interactive fallback in -p mode). pi has no
// approval/sandbox gate — the pod is the sandbox.
func (h *Harness) TaskCommand(prompt string, _ int) string {
	return "pi --provider poddle --model poddle-model -p " + shellSingleQuote(prompt)
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StateDirs is empty: pi's config (models.json) is rewritten by Provisions on each
// up/move, and its session state lives under the config dir — nothing needs a
// separate persisted volume for a first cut.
func (h *Harness) StateDirs() []string { return nil }

// ResumeCommand is empty: pi session resume is not yet wired, so a `move` recreates
// the shell rather than continuing the conversation. (Follow-up.)
func (h *Harness) ResumeCommand(mode string) string { return "" }

// EgressHosts is what pi needs to install and run: the npm registry and OpenAI's
// API host. Seeds the default-deny allow-list.
func (h *Harness) EgressHosts() []string {
	return []string{"registry.npmjs.org", "api.openai.com"}
}

// ConfigDir is PI_CODING_AGENT_DIR (/root/.pi), where pi keeps user config. poddle
// seeds a user's host config here and persists it as a named volume. models.json is
// poddle-owned (rewritten each up by Provisions to pin the broker provider); a user's
// mcp.json / extensions/ live alongside it and are the seed/persist target.
func (h *Harness) ConfigDir() string { return "/root/.pi" }

// MCPWiring registers a brokered MCP server with pi via the pi-mcp-adapter
// extension (installed in Provisions): it merges a remote entry into
// $PI_CODING_AGENT_DIR/mcp.json. `bearerTokenEnv` makes the adapter read the handle
// from the env var at connect time, so it never lands on disk. A node merge (node is
// present for pi) preserves any existing mcpServers. Runs after Provisions installs pi
// + the adapter. Verified against pi 0.84.3 + pi-mcp-adapter 2.28.0.
func (h *Harness) MCPWiring(name, agentURL, handleEnv string) []string {
	entry := `{"url":"` + agentURL + `","auth":"bearer","bearerTokenEnv":"` + handleEnv + `"}`
	js := `const f=process.env.PI_CODING_AGENT_DIR+"/mcp.json",fs=require("fs");let c={};try{c=JSON.parse(fs.readFileSync(f,"utf8"))}catch(e){};(c.mcpServers=c.mcpServers||{})["` + name + `"]=` + entry + `;fs.writeFileSync(f,JSON.stringify(c))`
	return []string{"node -e " + shellSingleQuote(js)}
}
