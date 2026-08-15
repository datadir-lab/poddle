// Package ls implements `poddle ls`: list sandboxes on the target engine.
package ls

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/sandbox"
)

// lister is the narrow provider capability this slice needs (the podman
// provider in production, a fake in tests).
type lister interface {
	List() ([]sandbox.Sandbox, error)
}

// NewCmd builds the ls command around a lister.
func NewCmd(p lister) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List sandboxes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := p.List()
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
