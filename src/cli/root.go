package main

import (
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"git.dev.datadir.co/datadir/poddle/src/cli/down"
	cliidentity "git.dev.datadir.co/datadir/poddle/src/cli/identity"
	"git.dev.datadir.co/datadir/poddle/src/cli/ls"
	"git.dev.datadir.co/datadir/poddle/src/cli/up"
	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/engine"
	"git.dev.datadir.co/datadir/poddle/src/internal/exec"
	"git.dev.datadir.co/datadir/poddle/src/internal/harness"
	"git.dev.datadir.co/datadir/poddle/src/internal/harness/claudecode"
	idn "git.dev.datadir.co/datadir/poddle/src/internal/identity"
	"git.dev.datadir.co/datadir/poddle/src/internal/identity/anthropic"
	"git.dev.datadir.co/datadir/poddle/src/internal/podman"
	"git.dev.datadir.co/datadir/poddle/src/internal/prompt"
)

// NewRootCmd builds the root poddle command and registers the feature slices.
// It is the composition root: the engine, identity store, and provider/harness
// registries are constructed once, bundled into an app.App, and injected into
// each command. The secretless broker is up-scoped (Phase 1), so it is passed
// only to `up`.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "poddle",
		Short:        "poddle — self-hostable, secret-safe agent dev environments",
		SilenceUsage: true,
	}

	// PODDLE_HOST empty = local podman; ssh://… = a remote host (same code path).
	var eng engine.Engine = podman.New(exec.OS{}, os.Getenv("PODDLE_HOST"))

	// Identities live on the client (never only in poddle). Providers are the
	// auth vendors, vertically sliced.
	store := idn.NewStore(idn.DefaultBase())
	reg := idn.Registry{
		"anthropic": anthropic.New(),
	}

	// Harnesses are the pod-side coding-agent runtimes (--harness).
	harnesses := harness.Registry{
		"claude-code": claudecode.New(),
	}

	// The composition root: one App, injected into every command.
	a := &app.App{
		Engine:     eng,
		Identities: store,
		Providers:  reg,
		Harnesses:  harnesses,
	}
	// Interactive prompts only on a real terminal; scripts/CI get no Prompter.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		a.Prompter = prompt.NewHuh()
	}

	root.AddCommand(ls.NewCmd(a))
	root.AddCommand(up.NewCmd(a, broker.NewBroker())) // broker is up-scoped (Phase 1)
	root.AddCommand(down.NewCmd(a))
	root.AddCommand(cliidentity.NewCmd(a))
	return root
}

// Execute builds and runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
