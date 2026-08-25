// Package identity implements `poddle identity` — manage coding-agent logins.
package identity

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
)

// NewCmd builds the `identity` command tree around the store and provider registry.
func NewCmd(a *app.App) *cobra.Command {
	c := &cobra.Command{
		Use:   "identity",
		Short: "Manage coding-agent logins (identities)",
	}
	c.AddCommand(
		addCmd(a.Identities, a.Providers),
		lsCmd(a.Identities, a.Providers),
		statusCmd(a.Identities, a.Providers),
		rmCmd(a.Identities),
	)
	return c
}

func addCmd(store *idn.Store, reg idn.Registry) *cobra.Command {
	var provider string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an identity by logging into a provider (client-side)",
		Example: `  # log into a provider once; the key stays on your machine
  poddle identity add work --provider anthropic`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, ok := reg.Get(provider)
			if !ok {
				return fmt.Errorf("unknown provider %q", provider)
			}
			id, err := store.Create(args[0], provider)
			if err != nil {
				return err
			}
			return p.Authenticate(id)
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "anthropic", "auth provider (anthropic|openai)")
	return cmd
}

func lsCmd(store *idn.Store, reg idn.Registry) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List identities",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			list, err := store.List()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tPROVIDER\tAUTHED")
			for _, id := range list {
				fmt.Fprintf(w, "%s\t%s\t%s\n", id.Name, id.Provider, authedLabel(reg, id))
			}
			return w.Flush()
		},
	}
}

func statusCmd(store *idn.Store, reg idn.Registry) *cobra.Command {
	return &cobra.Command{
		Use:     "status <name>",
		Short:   "Check whether an identity is authenticated",
		Example: `  poddle identity status work`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := store.Get(args[0])
			if err != nil {
				return err
			}
			p, ok := reg.Get(id.Provider)
			if !ok {
				return fmt.Errorf("unknown provider %q", id.Provider)
			}
			authed, err := p.IsAuthenticated(id)
			if err != nil {
				return err
			}
			if authed {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: authenticated\n", id.Name)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: NOT authenticated — run: poddle identity add %s\n", id.Name, id.Name)
			}
			return nil
		},
	}
}

func rmCmd(store *idn.Store) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Short:   "Remove an identity",
		Example: `  poddle identity rm work`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return store.Remove(args[0])
		},
	}
}

func authedLabel(reg idn.Registry, id idn.Identity) string {
	p, ok := reg.Get(id.Provider)
	if !ok {
		return "?"
	}
	authed, err := p.IsAuthenticated(id)
	if err != nil {
		return "?"
	}
	if authed {
		return "yes"
	}
	return "no"
}
