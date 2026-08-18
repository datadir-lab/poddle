// Package app holds poddle's composition-root type: the dependencies
// constructed once in the CLI root and handed to each command. It holds
// interfaces (so tests inject fakes) and contains no logic — pure wiring.
//
// The broker is deliberately NOT here: in Phase 1 each poddle command is a
// separate process, so a broker "shared across commands" is illusory. The
// broker is up-scoped now (constructed inside `up` for the attached session)
// and moves into poddled in Phase 2, at which point a poddled *client* — not
// the broker itself — is what belongs on App.
package app

import (
	"git.dev.datadir.co/datadir/poddle/src/internal/config"
	"git.dev.datadir.co/datadir/poddle/src/internal/connector"
	"git.dev.datadir.co/datadir/poddle/src/internal/engine"
	"git.dev.datadir.co/datadir/poddle/src/internal/harness"
	"git.dev.datadir.co/datadir/poddle/src/internal/identity"
	"git.dev.datadir.co/datadir/poddle/src/internal/policy"
	"git.dev.datadir.co/datadir/poddle/src/internal/prompt"
)

// App bundles the dependencies shared across commands.
type App struct {
	Engine        engine.Engine     // container/remote backend
	Identities    *identity.Store   // client-side identity store
	Providers     identity.Registry // auth vendors (anthropic, …)
	Harnesses     harness.Registry  // pod-side runtimes (claude-code, …)
	Prompter      prompt.Prompter   // interactive prompts; nil = non-interactive (no prompting)
	Templates     config.Resolver   // template resolver; nil = no templates
	Connections   *connector.Store  // client-side service connections
	ConnectorsDir string            // dir with user connector definitions (overrides built-ins)
	Policies      policy.Store      // governance policies (project poddle/ + global); nil = none
}
