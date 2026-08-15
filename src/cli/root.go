package main

import (
	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/cli/ls"
	"git.dev.datadir.co/datadir/poddle/src/internal/exec"
	"git.dev.datadir.co/datadir/poddle/src/internal/podman"
)

// NewRootCmd builds the root poddle command and registers the feature slices.
// It is the composition root: infrastructure (runner, provider) is constructed
// here once and injected into each slice.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "poddle",
		Short:        "poddle — self-hostable, secret-safe agent dev environments",
		SilenceUsage: true,
	}

	// conn "" = local Podman. Remote host (ssh://…) is wired from user config
	// in a later slice; the provider already supports it.
	provider := podman.New(exec.OS{}, "")

	root.AddCommand(ls.NewCmd(provider))
	return root
}

// Execute builds and runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
