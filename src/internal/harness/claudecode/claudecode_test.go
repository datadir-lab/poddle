package claudecode

import (
	"reflect"
	"strings"
	"testing"

	"git.dev.datadir.co/datadir/poddle/src/internal/harness"
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
	for _, w := range []string{"claude -p --continue", "IS_SANDBOX", "</dev/null"} {
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
