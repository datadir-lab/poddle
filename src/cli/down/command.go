// Package down implements `poddle down`: stop and remove a sandbox.
package down

import (
	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
)

// NewCmd builds the down command.
func NewCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "down <name|id>",
		Short: "Stop and remove a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.Engine.Remove(args[0])
		},
	}
}
