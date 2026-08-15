package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/cli/down"
	"github.com/datadir-lab/poddle/src/cli/ls"
	"github.com/datadir-lab/poddle/src/cli/up"
	"github.com/datadir-lab/poddle/src/internal/engine"
	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/podman"
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
	// (same code path — the provider just adds --url).
	var eng engine.Engine = podman.New(exec.OS{}, os.Getenv("PODDLE_HOST"))

	root.AddCommand(ls.NewCmd(eng))
	root.AddCommand(up.NewCmd(eng))
	root.AddCommand(down.NewCmd(eng))
	return root
}

// Execute builds and runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
