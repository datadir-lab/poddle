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

// Volume is a named volume mounted into the sandbox. Named volumes persist
// across pod recreation, so session state (workspace, agent state) survives a
// `poddle move` and is removed by `poddle down`.
type Volume struct {
	Name      string // engine volume name, e.g. poddle-proj-workspace
	Container string // mount path in the pod, e.g. /workspace
}

// PodInfo is a pod's reconstructable configuration, read back from its labels,
// so `move` (and the daemon's autoscaler) can recreate the shell preserving
// image / identity / harness / repo / mode / autoscale — without a working
// directory or template.
type PodInfo struct {
	Image     string
	Size      string
	Harness   string
	Identity  string
	Repo      string
	Mode      string
	Autoscale bool
}

// Stat is a running sandbox's live resource usage.
type Stat struct {
	Name    string // pod name
	CPU     string // CPU percent, e.g. "12.5%"
	Mem     string // memory usage / limit, e.g. "512MB / 4GB"
	MemPerc string // memory percent of the cap, e.g. "12.5%"
}

// Spec describes a sandbox to create.
type Spec struct {
	Name      string
	Image     string
	Template  string            // label
	Runtime   string            // label (default "container")
	Size      string            // label
	CPUs      float64           // 0 = leave unset
	Memory    string            // e.g. "16g"; "" = leave unset
	Repo      string            // label
	Mode      string            // label: how the agent runs (interactive | headless | exec) — drives resume on move
	Autoscale bool              // label poddle.autoscale: opt in to the daemon's reactive memory-grow autoscaler
	Identity  string            // label poddle.identity: the coding-agent login, so `move` can re-broker it
	Harness   string            // label poddle.harness: the agent runtime, so `move` recreates with the same one
	Mounts    []Mount           // credential/workspace mounts (e.g. an identity)
	Volumes   []Volume          // named volumes for session state (workspace, agent state)
	Env       map[string]string // env vars injected into the sandbox
	Setup     []string          // shell commands run in the pod after create (e.g. harness install)
}
