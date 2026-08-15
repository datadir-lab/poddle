package main

import (
	"os"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/cli/down"
	cliidentity "git.dev.datadir.co/datadir/poddle/src/cli/identity"
	"git.dev.datadir.co/datadir/poddle/src/cli/ls"
	"git.dev.datadir.co/datadir/poddle/src/cli/up"
	"git.dev.datadir.co/datadir/poddle/src/internal/engine"
	"git.dev.datadir.co/datadir/poddle/src/internal/exec"
	idn "git.dev.datadir.co/datadir/poddle/src/internal/identity"
	"git.dev.datadir.co/datadir/poddle/src/internal/identity/anthropic"
	"git.dev.datadir.co/datadir/poddle/src/internal/podman"
)

// NewRootCmd builds the root poddle command and registers the feature slices.
// It is the composition root: the engine, identity store, and provider registry
// are constructed here once and injected into each slice.
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

	root.AddCommand(ls.NewCmd(eng))
	root.AddCommand(up.NewCmd(eng, store, reg))
	root.AddCommand(down.NewCmd(eng))
	root.AddCommand(cliidentity.NewCmd(store, reg))
	return root
}

// Execute builds and runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
