package claudecode

import (
	"reflect"
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
