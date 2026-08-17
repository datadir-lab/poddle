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
}

// Registry maps harness names to implementations.
type Registry map[string]Harness

// Get returns the harness for name.
func (r Registry) Get(name string) (Harness, bool) {
	h, ok := r[name]
	return h, ok
}
