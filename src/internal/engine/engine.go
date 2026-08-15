// Package engine defines poddle's core capability contract. The same interface
// is implemented in-process by a container backend (podman) for local use, and
// (later) by a remote client talking to poddled for remote/cloud targets — so
// commands behave identically regardless of where sandboxes run.
package engine

import "git.dev.datadir.co/datadir/poddle/src/internal/sandbox"

// Engine is the set of operations poddle performs on sandboxes. It grows as
// commands are added (Create, Attach, Remove, ...).
type Engine interface {
	List() ([]sandbox.Sandbox, error)
}
