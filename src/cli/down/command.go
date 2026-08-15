// Package down implements `poddle down`: stop and remove a sandbox.
package down

import (
	"github.com/spf13/cobra"
)

// remover is the narrow provider capability this slice needs.
type remover interface {
	Remove(id string) error
}

// NewCmd builds the down command around a remover.
func NewCmd(e remover) *cobra.Command {
	return &cobra.Command{
		Use:   "down <name|id>",
		Short: "Stop and remove a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return e.Remove(args[0])
		},
	}
}
