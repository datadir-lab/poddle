package claudecode

import (
	"reflect"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/harness"
)

// Harness must satisfy the harness.Harness contract.
var _ harness.Harness = (*Harness)(nil)

func TestName(t *testing.T) {
	if got := New().Name(); got != "claude-code" {
		t.Errorf("name = %q, want claude-code", got)
	}
}

func TestSupports(t *testing.T) {
	h := New()
	if !h.Supports("anthropic") {
		t.Error("should support anthropic")
	}
	if h.Supports("openai") {
		t.Error("should not support openai")
	}
}

func TestProvisions(t *testing.T) {
	want := []string{"npm i -g @anthropic-ai/claude-code"}
	if got := New().Provisions(); !reflect.DeepEqual(got, want) {
		t.Errorf("provisions = %v, want %v", got, want)
	}
}

func TestResumeCommand(t *testing.T) {
	h := New()
	if got := h.ResumeCommand("interactive"); got != "claude --continue" {
		t.Errorf("interactive resume = %q", got)
	}
	hl := h.ResumeCommand("headless")
	for _, w := range []string{
		`claude -p 'continue where you left off' --continue`, // a nudge drives the turn
		"IS_SANDBOX",
		"--dangerously-skip-permissions",
		"</dev/null",
	} {
		if !strings.Contains(hl, w) {
			t.Errorf("headless resume missing %q:\n%s", w, hl)
		}
	}
}

func TestStateDirs(t *testing.T) {
	if got := New().StateDirs(); len(got) != 1 || got[0] != "/root/.claude" {
		t.Errorf("state dirs = %v, want [/root/.claude]", got)
	}
}

func TestTaskCommand(t *testing.T) {
	got := New().TaskCommand("fix the bug in it's parser", 5)
	for _, want := range []string{
		"IS_SANDBOX=1",
		"hasCompletedOnboarding",
		`claude -p 'fix the bug in it'\''s parser'`, // single-quote escaped
		"--max-turns 5",
		"--dangerously-skip-permissions",
		"</dev/null",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("task command missing %q in:\n%s", want, got)
		}
	}
	// default max-turns when unset
	if !strings.Contains(New().TaskCommand("x", 0), "--max-turns 24") {
		t.Error("expected a default max-turns")
	}
}

func TestEnv(t *testing.T) {
	env := New().Env("http://broker:9000", "poddle_abc")
	want := map[string]string{
		"ANTHROPIC_BASE_URL":   "http://broker:9000",
		"ANTHROPIC_AUTH_TOKEN": "poddle_abc",
	}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
}

func TestTaskCommand_MergesOnboardingNotClobber(t *testing.T) {
	cmd := New().TaskCommand("do a thing", 5)
	if strings.Contains(cmd, "> $HOME/.claude.json") {
		t.Errorf("TaskCommand must NOT overwrite ~/.claude.json (clobbers user MCP):\n%s", cmd)
	}
	for _, want := range []string{"node -e", "hasCompletedOnboarding", "readFileSync", "writeFileSync"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("TaskCommand onboarding must be a node merge (missing %q):\n%s", want, cmd)
		}
	}
}

func TestResumeCommand_HeadlessMergesOnboarding(t *testing.T) {
	cmd := New().ResumeCommand("headless")
	if strings.Contains(cmd, "> $HOME/.claude.json") {
		t.Errorf("headless resume must NOT overwrite ~/.claude.json:\n%s", cmd)
	}
	if !strings.Contains(cmd, "node -e") || !strings.Contains(cmd, "hasCompletedOnboarding") {
		t.Errorf("headless resume onboarding must be a node merge:\n%s", cmd)
	}
}

func TestMCPWiring_ClaudeMcpAdd(t *testing.T) {
	got := New().MCPWiring("linear", "http://10.0.0.5:9000/mcp", "PODDLE_MCP_LINEAR")
	if len(got) != 1 {
		t.Fatalf("want one Setup command, got %v", got)
	}
	for _, want := range []string{"claude mcp add", "--transport http", "'linear'", "http://10.0.0.5:9000/mcp", "Authorization: Bearer ${PODDLE_MCP_LINEAR}"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("MCPWiring missing %q: %q", want, got[0])
		}
	}
}
