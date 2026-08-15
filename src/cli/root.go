package main

import (
	"os"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/cli/ls"
	"git.dev.datadir.co/datadir/poddle/src/cli/up"
	"git.dev.datadir.co/datadir/poddle/src/internal/engine"
	"git.dev.datadir.co/datadir/poddle/src/internal/exec"
	"git.dev.datadir.co/datadir/poddle/src/internal/podman"
)

// NewRootCmd builds the root poddle command and registers the feature slices.
// It is the composition root: the engine is constructed here once and injected
// into each slice.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "poddle",
		Short:        "poddle — self-hostable, secret-safe agent dev environments",
		SilenceUsage: true,
	}

	// The engine. PODDLE_HOST empty = local, in-process, podman-backed; set to
	// ssh://user@host/run/user/<uid>/podman/podman.sock to target a remote host
	// (same code path — the provider just adds --url). A full config/target
	// slice and a remote poddled engine build on this seam later.
	var eng engine.Engine = podman.New(exec.OS{}, os.Getenv("PODDLE_HOST"))

	root.AddCommand(ls.NewCmd(eng))
	root.AddCommand(up.NewCmd(eng))
	return root
}

// Execute builds and runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
