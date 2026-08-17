// Package engine defines poddle's core capability contract. The same interface
// is implemented in-process by a container backend (podman) for local use, and
// (later) by a remote client talking to poddled for remote/cloud targets — so
// commands behave identically regardless of where sandboxes run.
package engine

import "github.com/datadir-lab/poddle/src/internal/sandbox"

// Engine is the set of operations poddle performs on sandboxes. It grows as
// commands are added.
type Engine interface {
	List() ([]sandbox.Sandbox, error)
	Stats() ([]sandbox.Stat, error) // live CPU/memory for running managed pods
	Create(spec sandbox.Spec) (id string, err error)
	Attach(id string) error
	Exec(id string, command string) error                // run a one-shot command in the sandbox (streams)
	ExecTTY(id string, command string) error             // run an interactive (TTY) command in the sandbox
	ExecDetached(id string, command string) error        // run a command in the background in the sandbox
	Resize(id string, cpus float64, memory string) error // live-update a running sandbox's cpu/memory
	PodInfo(id string) (sandbox.PodInfo, error)          // pod config read from labels, so move recreates it faithfully
	Remove(id string) error
	RemoveVolumesForPod(pod string) error // remove a pod's session-state volumes
}
