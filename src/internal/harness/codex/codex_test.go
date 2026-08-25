package codex

import (
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/harness"
)

var _ harness.Harness = (*Harness)(nil)

func TestName(t *testing.T) {
	if New().Name() != "codex" {
		t.Errorf("Name = %q", New().Name())
	}
}

func TestSupports(t *testing.T) {
	h := New()
	if !h.Supports("openai") {
		t.Error("codex must support openai")
	}
	if h.Supports("anthropic") {
		t.Error("codex must not support anthropic")
	}
}

func TestEnv_HandleAndConfigVars(t *testing.T) {
	env := New().Env("http://10.0.0.5:9000", "poddle_abc")
	if env["OPENAI_API_KEY"] != "poddle_abc" {
		t.Errorf("OPENAI_API_KEY = %q, want the handle", env["OPENAI_API_KEY"])
	}
	// Codex ignores OPENAI_BASE_URL; the broker URL travels via PODDLE_CODEX_BASE_URL
	// (which the -c provider flags read at run time), and must carry /v1.
	if env["PODDLE_CODEX_BASE_URL"] != "http://10.0.0.5:9000/v1" {
		t.Errorf("PODDLE_CODEX_BASE_URL = %q, want broker + /v1", env["PODDLE_CODEX_BASE_URL"])
	}
	if env["CODEX_HOME"] != "/root/.codex" {
		t.Errorf("CODEX_HOME = %q, want /root/.codex", env["CODEX_HOME"])
	}
}

func TestProvisions_InstallsCodexNoConfigTomlWrite(t *testing.T) {
	got := strings.Join(New().Provisions(), "\n")
	if !strings.Contains(got, "@openai/codex") {
		t.Errorf("provisions missing the codex npm install: %q", got)
	}
	// The provider now rides -c flags; codex must NOT write the user's config.toml.
	if strings.Contains(got, "config.toml") {
		t.Errorf("codex must no longer write config.toml (it is user-owned now):\n%s", got)
	}
}

func TestTaskCommand_InjectsProviderViaC(t *testing.T) {
	cmd := New().TaskCommand("do a thing", 5)
	for _, want := range []string{
		`-c 'model_provider="poddle"'`,
		"model_providers.poddle.base_url=", "$PODDLE_CODEX_BASE_URL",
		`model_providers.poddle.env_key="OPENAI_API_KEY"`,
		`model_providers.poddle.wire_api="responses"`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("TaskCommand missing %q:\n%s", want, cmd)
		}
	}
}

func TestTaskCommand_ExecOneShotSkipsGitCheckQuotesPrompt(t *testing.T) {
	cmd := New().TaskCommand("do a thing", 5)
	if !strings.Contains(cmd, "codex exec") {
		t.Errorf("TaskCommand = %q, want codex exec", cmd)
	}
	if !strings.Contains(cmd, "--skip-git-repo-check") {
		t.Errorf("TaskCommand must pass --skip-git-repo-check: %q", cmd)
	}
	// The pod is the sandbox; an autonomous task must be able to write files, so
	// Codex's read-only sandbox + approval prompts are bypassed (like claude-code's
	// --dangerously-skip-permissions). Without this, poddle task can't apply edits.
	if !strings.Contains(cmd, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("TaskCommand must bypass Codex's sandbox/approvals so it can edit: %q", cmd)
	}
	if !strings.Contains(cmd, "'do a thing'") {
		t.Errorf("TaskCommand must single-quote the prompt: %q", cmd)
	}
}

func TestTaskCommand_QuotesAdversarialPrompt(t *testing.T) {
	cmd := New().TaskCommand("it's a 'test'", 1)
	if strings.Contains(cmd, "it's a 'test'") { // must be escaped, not raw
		t.Errorf("prompt not escaped: %q", cmd)
	}
}

func TestStateDirs(t *testing.T) {
	if got := New().StateDirs(); len(got) != 1 || got[0] != "/root/.codex" {
		t.Errorf("StateDirs = %v, want [/root/.codex]", got)
	}
}

func TestResumeCommand(t *testing.T) {
	if !strings.Contains(New().ResumeCommand("interactive"), "resume") {
		t.Error("interactive resume must resume")
	}
	headless := New().ResumeCommand("headless")
	if !strings.Contains(headless, "codex exec") {
		t.Error("headless resume must use codex exec")
	}
	if !strings.Contains(headless, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("headless resume must run autonomously (bypass sandbox/approvals): %q", headless)
	}
	// Interactive hands the TTY back — the user approves, so it must NOT force-bypass.
	if strings.Contains(New().ResumeCommand("interactive"), "--dangerously-bypass") {
		t.Error("interactive resume must not force-bypass approvals")
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
	if got := New().ConfigDir(); got != "/root/.codex" {
		t.Errorf("ConfigDir = %q, want /root/.codex", got)
	}
}
