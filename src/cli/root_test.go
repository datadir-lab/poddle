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

func TestBuildRegistries_IncludeAider(t *testing.T) {
	_, harnesses := buildRegistries()
	if _, ok := harnesses.Get("aider"); !ok {
		t.Error("aider harness not registered")
	}
}

func TestBuildRegistries_IncludePi(t *testing.T) {
	_, harnesses := buildRegistries()
	if _, ok := harnesses.Get("pi"); !ok {
		t.Error("pi harness not registered")
	}
}

func TestBuildRegistries_IncludeOpencode(t *testing.T) {
	_, harnesses := buildRegistries()
	if _, ok := harnesses.Get("opencode"); !ok {
		t.Error("opencode harness not registered")
	}
}

func TestBuildRegistries_IncludeGoogleAndGemini(t *testing.T) {
	reg, harnesses := buildRegistries()
	if _, ok := reg.Get("google"); !ok {
		t.Error("google provider not registered")
	}
	if _, ok := harnesses.Get("gemini"); !ok {
		t.Error("gemini harness not registered")
	}
}

// TestBuildRegistries_MCPWiringMatrix pins the brokered-MCP support status of every
// registered harness in one place. codex, claude-code, opencode, and pi are wired —
// MCPWiring returns a Setup command carrying the broker MCP URL (pi via the
// pi-mcp-adapter extension installed in its Provisions).
//
// Two harnesses return nil, for DIFFERENT reasons:
//   - aider: no MCP-consumer support upstream at all (verified 2026-08-26: no
//     --mcp-server in any released version, only an open unmerged proposal) — a
//     PERMANENT gap.
//   - gemini: gemini-cli DOES support MCP (settings.json mcpServers), but poddle's
//     brokered-MCP wiring for it is a DEFERRED follow-up, so it is nil for now.
//
// Asserting the whole matrix here makes the support explicit and catches an
// accidental wiring (or a regression that drops one) for any harness.
func TestBuildRegistries_MCPWiringMatrix(t *testing.T) {
	_, harnesses := buildRegistries()
	const agentURL = "http://10.0.0.5:9000/mcp"
	for _, name := range []string{"codex", "claude-code", "opencode", "pi"} {
		h, ok := harnesses.Get(name)
		if !ok {
			t.Fatalf("harness %q not registered", name)
		}
		got := h.MCPWiring("srv", agentURL, "PODDLE_MCP_SRV")
		if len(got) == 0 {
			t.Errorf("%s should wire brokered MCP (non-nil Setup)", name)
			continue
		}
		if !strings.Contains(strings.Join(got, "\n"), agentURL) {
			t.Errorf("%s MCPWiring should reference the broker MCP url %q: %v", name, agentURL, got)
		}
	}
	for _, name := range []string{"aider", "gemini"} {
		h, ok := harnesses.Get(name)
		if !ok {
			t.Fatalf("harness %q not registered", name)
		}
		if got := h.MCPWiring("srv", agentURL, "PODDLE_MCP_SRV"); got != nil {
			t.Errorf("%s MCPWiring must be nil for now, got %v", name, got)
		}
	}
}
