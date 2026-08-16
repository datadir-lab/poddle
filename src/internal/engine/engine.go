// Package engine defines poddle's core capability contract. The same interface
// is implemented in-process by a container backend (podman) for local use, and
// (later) by a remote client talking to poddled for remote/cloud targets — so
// commands behave identically regardless of where sandboxes run.
package engine

import "git.dev.datadir.co/datadir/poddle/src/internal/sandbox"

// Engine is the set of operations poddle performs on sandboxes. It grows as
// commands are added.
type Engine interface {
	List() ([]sandbox.Sandbox, error)
	Create(spec sandbox.Spec) (id string, err error)
	Attach(id string) error
	Exec(id string, command string) error // run a one-shot command in the sandbox
	Remove(id string) error
}
