// Package ls implements `poddle ls`: list sandboxes on the target engine.
package ls

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

// NewCmd builds the ls command.
func NewCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List sandboxes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := a.Engine.List()
			if err != nil {
				return err
			}
			return render(cmd.OutOrStdout(), list)
		},
	}
}

func render(w io.Writer, list []sandbox.Sandbox) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTEMPLATE\tSIZE\tSTATE\tREPO")
	for _, s := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.ID, s.Name, s.Template, s.Size, s.State, s.Repo)
	}
	return tw.Flush()
}
