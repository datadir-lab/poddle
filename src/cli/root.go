package main

import (
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

	// The local engine: in-process, podman-backed. A remote engine (a client
	// talking to poddled) will implement this same engine.Engine interface for
	// remote targets — so commands behave identically wherever sandboxes run.
	var eng engine.Engine = podman.New(exec.OS{}, "")

	root.AddCommand(ls.NewCmd(eng))
	root.AddCommand(up.NewCmd(eng))
	return root
}

// Execute builds and runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
