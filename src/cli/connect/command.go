// Package connect implements `poddle connect` — manage service connections
// (git, CI, …) that the broker injects into pods secretlessly.
package connect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/connector"
	"github.com/datadir-lab/poddle/src/internal/oauth"
)

// oauthFlowTimeout bounds how long we wait for the user to complete the
// browser authorization step (and the surrounding discovery/exchange calls).
const oauthFlowTimeout = 5 * time.Minute

// placeholderRedirectURI is only used to ask a server whether it supports
// Dynamic Client Registration; the real redirect_uri (bound to the loopback
// listener AuthCodeFlow opens) is registered implicitly by most DCR servers
// accepting any http://127.0.0.1 redirect, and is what's actually sent to
// the authorization/token endpoints.
const placeholderRedirectURI = "http://127.0.0.1/callback"

// NewCmd builds the `connect` command tree.
func NewCmd(a *app.App) *cobra.Command {
	c := &cobra.Command{
		Use:   "connect",
		Short: "Manage service connections (git, CI, …) brokered into pods",
	}
	c.AddCommand(addCmd(a), lsCmd(a), rmCmd(a), reauthCmd(a))
	return c
}

func addCmd(a *app.App) *cobra.Command {
	var connName, url, user, token, clientID, clientSecret, scope string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a service connection (the token is brokered, never stored in a pod)",
		Example: `  # pipe the token on stdin so it never hits your shell history
  echo $GITHUB_TOKEN | poddle connect add github --connector forgejo --url https://git.acme.co

  # an OAuth-protected MCP server: omit --token and poddle probes + opens your browser
  poddle connect add my-mcp --connector mcp --url https://mcp.acme.co`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" { // read the token from stdin so it isn't in argv
				b, _ := io.ReadAll(cmd.InOrStdin())
				token = strings.TrimSpace(string(b))
			}
			if token != "" {
				_, err := a.Connections.Create(args[0], connName, url, user, token, "")
				return err
			}
			return addViaOAuth(cmd, a, args[0], connName, url, user, clientID, clientSecret, scope)
		},
	}
	cmd.Flags().StringVar(&connName, "connector", "", "connector type (forgejo, woodpecker, or a custom one)")
	cmd.Flags().StringVar(&url, "url", "", "service base URL")
	cmd.Flags().StringVar(&user, "user", "", "service username (for basic auth)")
	cmd.Flags().StringVar(&token, "token", "", "service token (or pipe it on stdin)")
	cmd.Flags().StringVar(&clientID, "client-id", "", "pre-registered OAuth client ID (required if the server has no Dynamic Client Registration)")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "OAuth client secret (confidential clients only; pairs with --client-id)")
	cmd.Flags().StringVar(&scope, "scope", "", "OAuth scope(s) to request")
	_ = cmd.MarkFlagRequired("connector")
	return cmd
}

// addViaOAuth is the no-token path of `connect add`: probe the service for
// OAuth protection, then run the PKCE authorization-code browser flow.
func addViaOAuth(cmd *cobra.Command, a *app.App, name, connName, url, user, clientID, clientSecret, scope string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), oauthFlowTimeout)
	defer cancel()

	m, err := oauth.Discover(ctx, http.DefaultClient, url)
	if err != nil {
		if errors.Is(err, oauth.ErrNoOAuth) {
			return fmt.Errorf("no token - pass --token or pipe it on stdin")
		}
		return fmt.Errorf("oauth discovery: %w", err)
	}

	if clientID == "" {
		clientID, clientSecret, err = oauth.Register(ctx, http.DefaultClient, m.RegistrationEndpoint, placeholderRedirectURI)
		if err != nil {
			if errors.Is(err, oauth.ErrNoDCR) {
				return fmt.Errorf("this MCP server needs a pre-registered OAuth client; pass --client-id [--client-secret]")
			}
			return fmt.Errorf("oauth client registration: %w", err)
		}
	}

	code, redirectURI, verifier, err := oauth.AuthCodeFlow(ctx, m, clientID, scope, oauth.OpenBrowser)
	if err != nil {
		return fmt.Errorf("oauth authorization: %w", err)
	}
	tok, err := oauth.Exchange(ctx, http.DefaultClient, m, clientID, clientSecret, code, verifier, redirectURI)
	if err != nil {
		return fmt.Errorf("oauth token exchange: %w", err)
	}

	if _, err := a.Connections.Create(name, connName, url, user, "", ""); err != nil {
		return err
	}
	if err := a.Connections.SaveOAuth(name, connector.OAuthMaterial{
		AccessToken:   tok.AccessToken,
		RefreshToken:  tok.RefreshToken,
		TokenEndpoint: m.TokenEndpoint,
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		Scope:         tok.Scope,
		ExpiresAt:     tok.ExpiresAt,
		RotatedAt:     time.Now(),
	}); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "added %s (OAuth)\n", name)
	return nil
}

// reauthCmd re-runs the OAuth browser flow for an existing OAuth connection —
// e.g. once its refresh token has expired or been revoked. It re-discovers
// the authorization server's endpoints from the connection's stored base URL
// (LoadOAuth doesn't persist AuthorizationEndpoint) and reuses the stored
// client credentials.
func reauthCmd(a *app.App) *cobra.Command {
	return &cobra.Command{
		Use:     "reauth <name>",
		Short:   "Re-run the OAuth browser flow for a connection",
		Example: `  poddle connect reauth my-mcp`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			conn, err := a.Connections.Get(name)
			if err != nil {
				return fmt.Errorf("connection %q not found: %w", name, err)
			}
			mat, ok, err := a.Connections.LoadOAuth(name)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("connection %q does not use OAuth (no --token connections need reauth)", name)
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), oauthFlowTimeout)
			defer cancel()

			m, err := oauth.Discover(ctx, http.DefaultClient, conn.BaseURL)
			if err != nil {
				return fmt.Errorf("oauth discovery: %w", err)
			}
			code, redirectURI, verifier, err := oauth.AuthCodeFlow(ctx, m, mat.ClientID, mat.Scope, oauth.OpenBrowser)
			if err != nil {
				return fmt.Errorf("oauth authorization: %w", err)
			}
			tok, err := oauth.Exchange(ctx, http.DefaultClient, m, mat.ClientID, mat.ClientSecret, code, verifier, redirectURI)
			if err != nil {
				return fmt.Errorf("oauth token exchange: %w", err)
			}
			if err := a.Connections.SaveOAuth(name, connector.OAuthMaterial{
				AccessToken:   tok.AccessToken,
				RefreshToken:  tok.RefreshToken,
				TokenEndpoint: m.TokenEndpoint,
				ClientID:      mat.ClientID,
				ClientSecret:  mat.ClientSecret,
				Scope:         tok.Scope,
				ExpiresAt:     tok.ExpiresAt,
				RotatedAt:     time.Now(),
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "reauthenticated %s (OAuth)\n", name)
			return nil
		},
	}
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
