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
	// PODDLE_OPENCODE_BASE_URL (read by the Provisions config-writer), with /v1.
	if env["PODDLE_OPENCODE_BASE_URL"] != "http://10.0.0.5:9000/v1" {
		t.Errorf("PODDLE_OPENCODE_BASE_URL = %q, want broker + /v1", env["PODDLE_OPENCODE_BASE_URL"])
	}
}

func TestProvisions_InstallsOpencodeAndWritesConfig(t *testing.T) {
	got := strings.Join(New().Provisions(), "\n")
	if !strings.Contains(got, "opencode-ai") {
		t.Errorf("provisions missing the opencode npm install: %q", got)
	}
	// Writes an opencode.json with a custom openai-compatible provider.
	for _, want := range []string{
		"$HOME/.config/opencode", "$PODDLE_OPENCODE_BASE_URL", "opencode.json",
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

func TestStateDirs_Empty(t *testing.T) {
	if got := New().StateDirs(); len(got) != 0 {
		t.Errorf("StateDirs = %v, want empty", got)
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
