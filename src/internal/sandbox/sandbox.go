// Package sandbox holds poddle's backend-agnostic domain types. Providers
// (podman today, kubernetes later) produce these; command slices consume them.
package sandbox

// Sandbox is a poddle instance, as surfaced by `poddle ls`.
type Sandbox struct {
	ID       string // short engine id
	Name     string // poddle.name label
	Template string // poddle.template label
	Runtime  string // poddle.runtime label (container | container-desktop | microvm | vm)
	Size     string // poddle.size label (weak | strong)
	Repo     string // poddle.repo label
	State    string // normalized: running | stopped | paused
}

// Mount is a host->container bind mount.
type Mount struct {
	Host      string
	Container string
	ReadOnly  bool
}

// Spec describes a sandbox to create.
type Spec struct {
	Name     string
	Image    string
	Template string            // label
	Runtime  string            // label (default "container")
	Size     string            // label
	CPUs     float64           // 0 = leave unset
	Memory   string            // e.g. "16g"; "" = leave unset
	Repo     string            // label
	Mounts   []Mount           // credential/workspace mounts (e.g. an identity)
	Env      map[string]string // env vars injected into the sandbox
	Setup    []string          // shell commands run in the pod after create (e.g. harness install)
}
