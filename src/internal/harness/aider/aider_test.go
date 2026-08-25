package aider

import (
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/harness"
)

var _ harness.Harness = (*Harness)(nil)

func TestName(t *testing.T) {
	if New().Name() != "aider" {
		t.Errorf("Name = %q", New().Name())
	}
}

func TestSupports(t *testing.T) {
	h := New()
	if !h.Supports("openai") {
		t.Error("aider must support openai")
	}
	if h.Supports("anthropic") {
		t.Error("aider (this slice) must not claim anthropic")
	}
}

func TestEnv_HandleAndV1Base(t *testing.T) {
	env := New().Env("http://10.0.0.5:9000", "poddle_abc")
	if env["OPENAI_API_KEY"] != "poddle_abc" {
		t.Errorf("OPENAI_API_KEY = %q, want the handle", env["OPENAI_API_KEY"])
	}
	// aider honors OPENAI_API_BASE directly; must carry /v1 (not auto-added).
	if env["OPENAI_API_BASE"] != "http://10.0.0.5:9000/v1" {
		t.Errorf("OPENAI_API_BASE = %q, want broker + /v1", env["OPENAI_API_BASE"])
	}
}

func TestProvisions_InstallsAider(t *testing.T) {
	got := strings.Join(New().Provisions(), " ")
	if !strings.Contains(got, "aider-chat") {
		t.Errorf("provisions = %q, want the aider-chat pip install", got)
	}
}

func TestTaskCommand_HeadlessQuotesPrompt(t *testing.T) {
	cmd := New().TaskCommand("do a thing", 5)
	for _, want := range []string{"aider", "--message", "--yes-always", "--no-stream", "--no-analytics", "'do a thing'"} {
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
	// aider keeps state in the workdir, not a separate home dir → stateless harness.
	if got := New().StateDirs(); len(got) != 0 {
		t.Errorf("StateDirs = %v, want empty (workdir-local state)", got)
	}
}

func TestResumeCommand(t *testing.T) {
	if !strings.Contains(New().ResumeCommand("interactive"), "--restore-chat-history") {
		t.Error("interactive resume must restore chat history")
	}
	headless := New().ResumeCommand("headless")
	if !strings.Contains(headless, "--restore-chat-history") || !strings.Contains(headless, "--message") {
		t.Errorf("headless resume must restore history and drive with --message: %q", headless)
	}
}

func TestEgressHosts(t *testing.T) {
	got := strings.Join(New().EgressHosts(), " ")
	for _, want := range []string{"pypi.org", "files.pythonhosted.org", "api.openai.com"} {
		if !strings.Contains(got, want) {
			t.Errorf("EgressHosts missing %q: %v", want, got)
		}
	}
}
