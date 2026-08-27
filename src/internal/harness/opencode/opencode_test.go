package opencode

import (
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/harness"
)

var _ harness.Harness = (*Harness)(nil)

func TestName(t *testing.T) {
	if New().Name() != "opencode" {
		t.Errorf("Name = %q", New().Name())
	}
}

func TestSupports(t *testing.T) {
	h := New()
	if !h.Supports("openai") {
		t.Error("opencode must support openai")
	}
	if h.Supports("anthropic") {
		t.Error("opencode (this slice) must not claim anthropic")
	}
}

func TestEnv_HandleAndBaseURL(t *testing.T) {
	env := New().Env("http://10.0.0.5:9000", "poddle_abc")
	if env["OPENAI_API_KEY"] != "poddle_abc" {
		t.Errorf("OPENAI_API_KEY = %q, want the handle", env["OPENAI_API_KEY"])
	}
	// opencode has no base-URL env var; the broker /v1 base travels via
	// PODDLE_OPENCODE_BASE_URL (read by the Provisions config-writer via
	// OPENCODE_CONFIG), with /v1.
	if env["PODDLE_OPENCODE_BASE_URL"] != "http://10.0.0.5:9000/v1" {
		t.Errorf("PODDLE_OPENCODE_BASE_URL = %q, want broker + /v1", env["PODDLE_OPENCODE_BASE_URL"])
	}
}

func TestEnv_SetsOpencodeConfigLayer(t *testing.T) {
	env := New().Env("http://10.0.0.5:9000", "poddle_abc")
	if env["OPENCODE_CONFIG"] == "" {
		t.Error("Env must set OPENCODE_CONFIG to poddle's dedicated provider layer")
	}
}

func TestProvisions_InstallsOpencodeAndWritesConfig(t *testing.T) {
	got := strings.Join(New().Provisions(), "\n")
	if !strings.Contains(got, "opencode-ai") {
		t.Errorf("provisions missing the opencode npm install: %q", got)
	}
	// Writes poddle's provider to the dedicated OPENCODE_CONFIG layer, NOT the
	// user's global ~/.config/opencode/opencode.json.
	if strings.Contains(got, ".config/opencode/opencode.json") {
		t.Errorf("opencode must not write the user's global config; use $OPENCODE_CONFIG:\n%s", got)
	}
	for _, want := range []string{
		"$OPENCODE_CONFIG", "$PODDLE_OPENCODE_BASE_URL",
		`@ai-sdk/openai-compatible`, `"apiKey":"{env:OPENAI_API_KEY}"`, `"baseURL":"%s"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("provisions missing %q in the config-writer:\n%s", want, got)
		}
	}
}

func TestTaskCommand_HeadlessQuotesPrompt(t *testing.T) {
	cmd := New().TaskCommand("do a thing", 5)
	for _, want := range []string{"opencode run", "-m poddle/poddle-model", "--format json", "--auto", "'do a thing'"} {
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

func TestStateDirs_SessionDBVolume(t *testing.T) {
	got := New().StateDirs()
	want := []string{"/root/.local/share/opencode"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("StateDirs = %v, want %v", got, want)
	}
}

func TestResumeCommand_Headless(t *testing.T) {
	cmd := New().ResumeCommand("headless")
	for _, want := range []string{"opencode run", "-c", "--format json", "continue where you left off"} {
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
}

func TestEgressHosts(t *testing.T) {
	got := strings.Join(New().EgressHosts(), " ")
	// opencode's wider first-run footprint: install + LLM + rg (github) + catalog.
	for _, want := range []string{"registry.npmjs.org", "api.openai.com", "github.com", "models.dev"} {
		if !strings.Contains(got, want) {
			t.Errorf("EgressHosts missing %q: %v", want, got)
		}
	}
}

func TestConfigDir(t *testing.T) {
	if got := New().ConfigDir(); got != "/root/.config/opencode" {
		t.Errorf("ConfigDir = %q, want /root/.config/opencode", got)
	}
}

func TestMCPWiring_OpencodeLayerMerge(t *testing.T) {
	got := New().MCPWiring("linear", "http://10.0.0.5:9000/mcp", "PODDLE_MCP_LINEAR")
	if len(got) != 1 {
		t.Fatalf("want one Setup command, got %v", got)
	}
	for _, want := range []string{"node -e", "OPENCODE_CONFIG", `"type":"remote"`, "http://10.0.0.5:9000/mcp", "Bearer {env:PODDLE_MCP_LINEAR}", `"linear"`} {
		if !strings.Contains(got[0], want) {
			t.Errorf("MCPWiring missing %q: %q", want, got[0])
		}
	}
}
