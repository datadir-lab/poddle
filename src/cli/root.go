package main

import "github.com/spf13/cobra"

// NewRootCmd builds the root poddle command. Feature slices register
// themselves onto it (one AddCommand line each) from newRootCmd's caller.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "poddle",
		Short:        "poddle — self-hostable, secret-safe agent dev environments",
		SilenceUsage: true,
	}
	return root
}

// Execute builds and runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
