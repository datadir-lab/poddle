package gemini

import (
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/harness"
)

var _ harness.Harness = (*Harness)(nil)

func TestName(t *testing.T) {
	if New().Name() != "gemini" {
		t.Errorf("Name = %q", New().Name())
	}
}

func TestSupports(t *testing.T) {
	h := New()
	if !h.Supports("google") {
		t.Error("gemini must support google")
	}
	if h.Supports("openai") || h.Supports("anthropic") {
		t.Error("gemini must not claim openai/anthropic")
	}
}

func TestEnv_SecretlessBrokerWiring(t *testing.T) {
	env := New().Env("http://10.0.0.5:9000/", "poddle_abc")
	if env["GEMINI_API_KEY"] != "poddle_abc" {
		t.Errorf("GEMINI_API_KEY = %q, want the handle", env["GEMINI_API_KEY"])
	}
	if env["GEMINI_API_KEY_AUTH_MECHANISM"] != "bearer" {
		t.Errorf("GEMINI_API_KEY_AUTH_MECHANISM = %q, want bearer (so the handle rides Authorization: Bearer)", env["GEMINI_API_KEY_AUTH_MECHANISM"])
	}
	// Base URL is the broker ORIGIN (no /v1beta) with any trailing slash trimmed —
	// the @google/genai SDK appends the /v1beta/models/... path itself.
	if env["GOOGLE_GEMINI_BASE_URL"] != "http://10.0.0.5:9000" {
		t.Errorf("GOOGLE_GEMINI_BASE_URL = %q, want the broker origin with no trailing slash", env["GOOGLE_GEMINI_BASE_URL"])
	}
	if env["GEMINI_CLI_TRUST_WORKSPACE"] != "true" {
		t.Errorf("GEMINI_CLI_TRUST_WORKSPACE = %q, want true (else gemini refuses to run)", env["GEMINI_CLI_TRUST_WORKSPACE"])
	}
	if env["GEMINI_SANDBOX"] != "false" {
		t.Errorf("GEMINI_SANDBOX = %q, want false", env["GEMINI_SANDBOX"])
	}
	if env["GEMINI_MODEL"] == "" {
		t.Error("GEMINI_MODEL should carry a default model")
	}
}

func TestProvisions_InstallsGeminiAndSelectsAuth(t *testing.T) {
	got := strings.Join(New().Provisions(), "\n")
	if !strings.Contains(got, "@google/gemini-cli") {
		t.Errorf("provisions missing the gemini-cli npm install: %q", got)
	}
	if !strings.Contains(got, "--ignore-scripts") {
		t.Errorf("provisions must npm install --ignore-scripts: %q", got)
	}
	// Selects the API-key auth path via the nested settings key (env var alone fails).
	for _, want := range []string{".gemini/settings.json", "security", "selectedType", "gemini-api-key"} {
		if !strings.Contains(got, want) {
			t.Errorf("provisions settings-merge missing %q: %q", want, got)
		}
	}
	// It must MERGE, not clobber (reads the existing file first).
	if !strings.Contains(got, "readFileSync") {
		t.Errorf("provisions must merge existing settings, not overwrite: %q", got)
	}
}

func TestTaskCommand_HeadlessRecipe(t *testing.T) {
	cmd := New().TaskCommand("say 'hi'", 0)
	for _, want := range []string{"gemini -p", "--output-format json", "--yolo", "</dev/null"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("TaskCommand missing %q: %q", want, cmd)
		}
	}
	// The prompt's single quote must be escaped so the shell command stays valid.
	if !strings.Contains(cmd, `'\''`) {
		t.Errorf("TaskCommand did not escape the prompt's quote: %q", cmd)
	}
}

func TestEgressHosts(t *testing.T) {
	hosts := strings.Join(New().EgressHosts(), ",")
	for _, want := range []string{"registry.npmjs.org", "generativelanguage.googleapis.com"} {
		if !strings.Contains(hosts, want) {
			t.Errorf("EgressHosts missing %q: %q", want, hosts)
		}
	}
}

func TestConfigDirAndStateDirs(t *testing.T) {
	h := New()
	if h.ConfigDir() != "/root/.gemini" {
		t.Errorf("ConfigDir = %q, want /root/.gemini", h.ConfigDir())
	}
	// StateDirs must be empty so it does not collide with the ConfigDir volume.
	if len(h.StateDirs()) != 0 {
		t.Errorf("StateDirs = %v, want empty (state lives under ConfigDir)", h.StateDirs())
	}
}

// MCPWiring is a deferred follow-up for gemini (unlike aider it is not a permanent
// gap), so it returns nil for now.
func TestMCPWiring_NilForNow(t *testing.T) {
	if got := New().MCPWiring("srv", "http://x/mcp", "PODDLE_MCP_SRV"); got != nil {
		t.Errorf("MCPWiring should be nil (deferred), got %v", got)
	}
}
