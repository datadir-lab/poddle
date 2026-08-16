// Package attach implements `poddle attach`: reconnect to a running sandbox.
// With the broker in poddled, a pod's secretless creds stay live after the
// original `up` exits — so attach just re-opens a session on the container.
package attach

import (
	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
)

// NewCmd builds the attach command.
func NewCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "attach <name|id>",
		Short: "Reconnect to a running sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.Engine.Attach(args[0])
		},
	}
}
