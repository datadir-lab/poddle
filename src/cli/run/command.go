// Package run implements `poddle run`: run a one-shot command in a running
// sandbox. The pod's brokered creds (via poddled) are live, so the command has
// the same secretless access as an interactive session.
package run

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
)

// NewCmd builds the run command.
func NewCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:                "run <name|id> <command>...",
		Short:              "Run a command in a running sandbox",
		Args:               cobra.MinimumNArgs(2),
		DisableFlagParsing: true, // pass the command through verbatim
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.Engine.Exec(args[0], strings.Join(args[1:], " "))
		},
	}
}
