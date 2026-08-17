// Package stats implements `poddle stats`: live CPU/memory for running pods.
package stats

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
)

// NewCmd builds the stats command.
func NewCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show live CPU/memory for running pods",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stats, err := a.Engine.Stats()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tCPU\tMEM\tMEM%")
			for _, s := range stats {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.CPU, s.Mem, s.MemPerc)
			}
			return tw.Flush()
		},
	}
}
