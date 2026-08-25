package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCmd_ShowsUsage(t *testing.T) {
	c := NewRootCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"--help"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "poddle") {
		t.Fatalf("usage missing 'poddle': %q", out.String())
	}
}

func TestRootCmd_RegistersSubcommands(t *testing.T) {
	found := map[string]bool{}
	for _, c := range NewRootCmd().Commands() {
		found[c.Name()] = true
	}
	for _, name := range []string{"ls", "up", "down", "identity", "connect"} {
		if !found[name] {
			t.Errorf("subcommand %q not registered by the composition root", name)
		}
	}
}

func TestBuildRegistries_IncludeOpenAIAndCodex(t *testing.T) {
	reg, harnesses := buildRegistries()
	for _, p := range []string{"anthropic", "openai"} {
		if _, ok := reg.Get(p); !ok {
			t.Errorf("provider %q not registered", p)
		}
	}
	for _, h := range []string{"claude-code", "codex"} {
		if _, ok := harnesses.Get(h); !ok {
			t.Errorf("harness %q not registered", h)
		}
	}
}
