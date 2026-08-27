package pi

import (
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/harness"
)

var _ harness.Harness = (*Harness)(nil)

func TestName(t *testing.T) {
	if New().Name() != "pi" {
		t.Errorf("Name = %q", New().Name())
	}
}

func TestSupports(t *testing.T) {
	h := New()
	if !h.Supports("openai") {
		t.Error("pi must support openai")
	}
	if h.Supports("anthropic") {
		t.Error("pi (this slice) must not claim anthropic")
	}
}

func TestEnv_HandleAndConfigVars(t *testing.T) {
	env := New().Env("http://10.0.0.5:9000", "poddle_abc")
	if env["OPENAI_API_KEY"] != "poddle_abc" {
		t.Errorf("OPENAI_API_KEY = %q, want the handle", env["OPENAI_API_KEY"])
	}
	// pi has no base-URL env var; the broker /v1 base travels via PODDLE_PI_BASE_URL
	// (read by the Provisions config-writer) and must carry /v1.
	if env["PODDLE_PI_BASE_URL"] != "http://10.0.0.5:9000/v1" {
		t.Errorf("PODDLE_PI_BASE_URL = %q, want broker + /v1", env["PODDLE_PI_BASE_URL"])
	}
	if env["PI_CODING_AGENT_DIR"] != "/root/.pi" {
		t.Errorf("PI_CODING_AGENT_DIR = %q, want /root/.pi", env["PI_CODING_AGENT_DIR"])
	}
	if env["PI_OFFLINE"] != "1" {
		t.Errorf("PI_OFFLINE = %q, want 1", env["PI_OFFLINE"])
	}
	// The pi-mcp-adapter must read only poddle's mcp.json, not stray ~/.config/mcp etc.
	if env["PI_MCP_CONFIG_MODE"] != "exclusive" {
		t.Errorf("PI_MCP_CONFIG_MODE = %q, want exclusive", env["PI_MCP_CONFIG_MODE"])
	}
}

func TestProvisions_InstallsPiAndWritesConfig(t *testing.T) {
	got := strings.Join(New().Provisions(), "\n")
	if !strings.Contains(got, "@earendil-works/pi-coding-agent") {
		t.Errorf("provisions missing the pi npm install: %q", got)
	}
	if !strings.Contains(got, "--ignore-scripts") {
		t.Errorf("provisions must npm install --ignore-scripts: %q", got)
	}
	// Writes a models.json redirecting pi to the broker via a custom provider.
	for _, want := range []string{
		"$PI_CODING_AGENT_DIR", "$PODDLE_PI_BASE_URL", "models.json",
		`"api":"openai-completions"`, `"apiKey":"$OPENAI_API_KEY"`, `"baseUrl":"%s"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("provisions missing %q in the config-writer:\n%s", want, got)
		}
	}
	// Installs the pi-mcp-adapter extension so MCPWiring's mcp.json is honored.
	if !strings.Contains(got, "pi install npm:pi-mcp-adapter") {
		t.Errorf("provisions missing the pi-mcp-adapter install: %q", got)
	}
}

func TestTaskCommand_HeadlessQuotesPrompt(t *testing.T) {
	cmd := New().TaskCommand("do a thing", 5)
	for _, want := range []string{"pi ", "--provider poddle", "--model poddle-model", "-p ", "'do a thing'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("TaskCommand missing %q: %q", want, cmd)
		}
	}
}

func TestTaskCommand_QuotesAdversarialPrompt(t *testing.T) {
	cmd := New().TaskCommand("it's a 'test'", 1)
	if strings.Contains(cmd, "it's a 'test'") { // must be escaped, not raw
		t.Errorf("prompt not escaped: %q", cmd)
	}
}

func TestStateDirs_Empty(t *testing.T) {
	if got := New().StateDirs(); len(got) != 0 {
		t.Errorf("StateDirs = %v, want empty", got)
	}
}

func TestResumeCommand_Headless(t *testing.T) {
	cmd := New().ResumeCommand("headless")
	// " -p " (space-bounded) checked separately from "--provider", which also
	// contains the bare substring "-p".
	for _, want := range []string{" -p ", "-c", "continue where you left off"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("headless ResumeCommand missing %q: %q", want, cmd)
		}
	}
}

func TestResumeCommand_Interactive(t *testing.T) {
	cmd := New().ResumeCommand("interactive")
	if !strings.Contains(cmd, "-c") {
		t.Errorf("interactive ResumeCommand missing -c: %q", cmd)
	}
	// " -p " (space-bounded) so "--provider" (which also contains the bare
	// substring "-p") does not produce a false positive.
	if strings.Contains(cmd, " -p ") {
		t.Errorf("interactive ResumeCommand must not carry -p: %q", cmd)
	}
}

func TestEgressHosts(t *testing.T) {
	got := strings.Join(New().EgressHosts(), " ")
	for _, want := range []string{"registry.npmjs.org", "api.openai.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("EgressHosts missing %q: %v", want, got)
		}
	}
}

func TestConfigDir(t *testing.T) {
	if got := New().ConfigDir(); got != "/root/.pi" {
		t.Errorf("ConfigDir = %q, want /root/.pi", got)
	}
}

func TestMCPWiring_PiAdapterEntry(t *testing.T) {
	got := New().MCPWiring("linear", "http://10.0.0.5:9000/mcp", "PODDLE_MCP_LINEAR")
	if len(got) != 1 {
		t.Fatalf("want one Setup command, got %v", got)
	}
	// mcpServers is referenced as a bare JS property (c.mcpServers), not a quoted
	// JSON key, in the merge one-liner — see MCPWiring's spike-verified js.
	for _, want := range []string{"node -e", "PI_CODING_AGENT_DIR", "mcp.json", "mcpServers", "http://10.0.0.5:9000/mcp", `"auth":"bearer"`, `"bearerTokenEnv":"PODDLE_MCP_LINEAR"`, "linear"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("MCPWiring missing %q: %q", want, got[0])
		}
	}
}
