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
