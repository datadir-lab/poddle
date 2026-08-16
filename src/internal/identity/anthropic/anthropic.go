// Package anthropic implements the identity.Provider for Claude Code
// (Anthropic subscription logins). It drives `claude setup-token` to capture a
// long-lived OAuth token and materializes it as CLAUDE_CODE_OAUTH_TOKEN — the
// recommended way to reuse a subscription in a container (portable, not
// machine-bound, no credential file to mount).
package anthropic

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/identity"
)

const tokenFile = "anthropic-token"

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Name() string { return "anthropic" }

func (p *Provider) tokenPath(id identity.Identity) string {
	return filepath.Join(id.Dir(), tokenFile)
}

// Authenticate runs `claude setup-token` interactively (the user logs in via
// browser/code); it prints a long-lived OAuth token on stdout, which we store
// 0600. The interactive run is verified against real Claude Code on a host.
func (p *Provider) Authenticate(id identity.Identity) error {
	cmd := exec.Command("claude", "setup-token")
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr // login prompts go to the user's terminal
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("claude setup-token: %w", err)
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return fmt.Errorf("claude setup-token returned no token")
	}
	return os.WriteFile(p.tokenPath(id), []byte(token), 0o600)
}

// IsAuthenticated reports whether a token is stored for this identity. (A live
// verification against the API is a planned upgrade.)
func (p *Provider) IsAuthenticated(id identity.Identity) (bool, error) {
	_, err := os.Stat(p.tokenPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Credential returns the stored subscription token as a broker.Credential for
// the broker to hold. The token is read plaintext here and sealed into the
// vault by the caller (broker.Store) — the same unavoidable boundary as Get.
func (p *Provider) Credential(id identity.Identity) (broker.Credential, error) {
	b, err := os.ReadFile(p.tokenPath(id))
	if err != nil {
		return broker.Credential{}, fmt.Errorf("read token: %w", err)
	}
	return broker.Credential{
		Mode:    broker.ModeSubscription,
		Vendor:  "anthropic",
		Secret:  strings.TrimSpace(string(b)),
		BaseURL: "https://api.anthropic.com",
	}, nil
}

// Materialize injects the stored token as CLAUDE_CODE_OAUTH_TOKEN.
func (p *Provider) Materialize(id identity.Identity) (identity.Materialization, error) {
	b, err := os.ReadFile(p.tokenPath(id))
	if err != nil {
		return identity.Materialization{}, fmt.Errorf("read token: %w", err)
	}
	return identity.Materialization{
		Env: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": strings.TrimSpace(string(b))},
	}, nil
}
