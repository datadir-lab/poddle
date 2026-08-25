// Package harness models the coding-agent runtime that runs INSIDE a pod
// (claude-code, codex, aider, pi) — the pod-side counterpart to an
// identity.Provider (client-side auth). A harness knows how to install itself,
// which auth vendors it can use, and how to point itself at the broker with a
// handle. It deals only in strings (broker address, handle, vendor); it knows
// nothing about the broker's internals.
package harness

// Harness is a swappable pod-side coding agent.
type Harness interface {
	Name() string
	// Provisions are shell commands that install the harness in a fresh pod.
	Provisions() []string
	// Supports reports whether the harness can use an auth vendor (e.g.
	// claude-code supports "anthropic").
	Supports(vendor string) bool
	// Env is the pod environment that points the harness at the broker at
	// brokerAddr, presenting handle instead of any real secret.
	Env(brokerAddr, handle string) map[string]string
	// TaskCommand is the shell command that runs the agent headless on prompt
	// to completion (non-interactive), for `poddle task`. maxTurns bounds the
	// agent's loop. Returns "" if the harness has no headless mode.
	TaskCommand(prompt string, maxTurns int) string
	// StateDirs are pod paths holding the agent's persistable state (e.g.
	// conversation history). They become named volumes so the session survives
	// `poddle move`. Empty for a stateless harness.
	StateDirs() []string
	// ResumeCommand is the shell command that resumes the agent's most recent
	// conversation after a `poddle move`, in the given mode ("interactive" or
	// "headless"). It reads the state dirs the move carried over. Empty if the
	// harness can't resume (move then just recreates the shell).
	ResumeCommand(mode string) string
	// EgressHosts are the hosts this harness must reach to install and run
	// itself (e.g. its package registry, its vendor's endpoints), beyond the
	// pod's identity API and connectors. They seed the default-deny egress
	// allow-list for a brokered pod that has no explicit policy, so the agent
	// works out of the box while exfiltration to unrelated hosts stays blocked.
	// Exact hosts, or ".suffix" for any subdomain.
	EgressHosts() []string
	// ConfigDir is the pod directory holding the agent's user-customizable config
	// (settings, plugins, MCP declarations). poddle seeds a user's host config
	// (~/.config/poddle/harness/<name>/) into it and persists it as a named volume
	// so customizations survive `move`. Empty = the harness has no seed/persist dir.
	ConfigDir() string
	// MCPWiring returns pod Setup commands that register a brokered MCP server
	// named `name`, reachable at `agentURL` (the broker gateway root + the server's
	// endpoint path), presenting the handle held in env var `handleEnv` as its
	// bearer token — via the agent's own MCP-registration channel, without
	// clobbering the user's config. nil = this harness has no MCP auto-wiring yet.
	MCPWiring(name, agentURL, handleEnv string) []string
}

// Registry maps harness names to implementations.
type Registry map[string]Harness

// Get returns the harness for name.
func (r Registry) Get(name string) (Harness, bool) {
	h, ok := r[name]
	return h, ok
}
