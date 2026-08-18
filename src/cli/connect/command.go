// Package connect implements `poddle connect` — manage service connections
// (git, CI, …) that the broker injects into pods secretlessly.
package connect

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
)

// NewCmd builds the `connect` command tree.
func NewCmd(a *app.App) *cobra.Command {
	c := &cobra.Command{
		Use:   "connect",
		Short: "Manage service connections (git, CI, …) brokered into pods",
	}
	c.AddCommand(addCmd(a), lsCmd(a), rmCmd(a))
	return c
}

func addCmd(a *app.App) *cobra.Command {
	var connector, url, user, token string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a service connection (the token is brokered, never stored in a pod)",
		Example: `  # pipe the token on stdin so it never hits your shell history
  echo $GITHUB_TOKEN | poddle connect add github --connector forgejo --url https://git.acme.co`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" { // read the token from stdin so it isn't in argv
				b, _ := io.ReadAll(cmd.InOrStdin())
				token = strings.TrimSpace(string(b))
			}
			if token == "" {
				return fmt.Errorf("no token — pass --token or pipe it on stdin")
			}
			_, err := a.Connections.Create(args[0], connector, url, user, token, "")
			return err
		},
	}
	cmd.Flags().StringVar(&connector, "connector", "", "connector type (forgejo, woodpecker, or a custom one)")
	cmd.Flags().StringVar(&url, "url", "", "service base URL")
	cmd.Flags().StringVar(&user, "user", "", "service username (for basic auth)")
	cmd.Flags().StringVar(&token, "token", "", "service token (or pipe it on stdin)")
	_ = cmd.MarkFlagRequired("connector")
	return cmd
}

func lsCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List service connections",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := a.Connections.List()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tCONNECTOR\tURL\tOWNER")
			for _, c := range list {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.Name, c.Connector, c.BaseURL, c.Owner)
			}
			return w.Flush()
		},
	}
}

func rmCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Short:   "Remove a service connection",
		Example: `  poddle connect rm github`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.Connections.Remove(args[0])
		},
	}
}
