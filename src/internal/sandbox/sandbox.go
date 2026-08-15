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
