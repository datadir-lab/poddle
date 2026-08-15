package identity

// Provider is an auth vendor — anthropic, openai, local. Each is a vertical
// slice. Authenticate and IsAuthenticated run on the CLIENT (where the human
// and browser are); Materialize describes how to inject the identity's creds
// into a sandbox.
type Provider interface {
	Name() string
	Authenticate(id Identity) error
	IsAuthenticated(id Identity) (bool, error)
	Materialize(id Identity) (Materialization, error)
}

// Materialization is how an identity is injected into a pod: credential mounts
// and/or environment (e.g. a local-LLM base URL + key).
type Materialization struct {
	Mounts []Mount
	Env    map[string]string
}

// Mount is a host->container credential mount.
type Mount struct {
	Host      string
	Container string
	ReadOnly  bool
}

// Registry maps provider names to their implementations.
type Registry map[string]Provider

// Get returns the provider for name.
func (r Registry) Get(name string) (Provider, bool) {
	p, ok := r[name]
	return p, ok
}
