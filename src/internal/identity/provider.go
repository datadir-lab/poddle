package identity

import "git.dev.datadir.co/datadir/poddle/src/internal/broker"

// Provider is an auth vendor — anthropic, openai, local. Each is a vertical
// slice. Authenticate and IsAuthenticated run on the CLIENT (where the human
// and browser are). Credential yields the real secret for the broker to hold —
// the secret never enters a pod.
type Provider interface {
	Name() string
	Authenticate(id Identity) error
	IsAuthenticated(id Identity) (bool, error)
	Credential(id Identity) (broker.Credential, error)
}

// Registry maps provider names to their implementations.
type Registry map[string]Provider

// Get returns the provider for name.
func (r Registry) Get(name string) (Provider, bool) {
	p, ok := r[name]
	return p, ok
}
