// Package openai implements the identity.Provider for OpenAI Codex. It holds a
// real OpenAI API key and hands it to the broker as a Bearer credential; the key
// never enters a pod. (ChatGPT-subscription / CODEX_ACCESS_TOKEN auth is a
// documented follow-up — Codex validates that token client-side and targets a
// different backend, so it cannot ride poddle's handle-swap.)
package openai

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/identity"
)

const tokenFile = "openai-token"

// stdin is injected for testing; in production it is os.Stdin.
var stdin io.Reader = os.Stdin

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Name() string { return "openai" }

func (p *Provider) tokenPath(id identity.Identity) string {
	return filepath.Join(id.Dir(), tokenFile)
}

// Authenticate captures the API key non-interactively: OPENAI_API_KEY, else one
// line from stdin. No browser, no TTY dependency (agent/CI shells have no pinentry).
func (p *Provider) Authenticate(id identity.Identity) error {
	token := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if token == "" {
		fmt.Fprint(os.Stderr, "Paste an OpenAI API key (sk-...): ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		token = strings.TrimSpace(line)
	}
	if token == "" {
		return fmt.Errorf("no OpenAI API key provided (set OPENAI_API_KEY)")
	}
	return os.WriteFile(p.tokenPath(id), []byte(token), 0o600)
}

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

// Credential returns the stored key as a Bearer (ModeSubscription) credential.
// BaseURL has NO path — the codex harness points Codex at <broker>/v1, and the
// request path carries /v1 through to the real upstream.
func (p *Provider) Credential(id identity.Identity) (broker.Credential, error) {
	b, err := os.ReadFile(p.tokenPath(id))
	if err != nil {
		return broker.Credential{}, fmt.Errorf("read token: %w", err)
	}
	// PODDLE_OPENAI_BASE_URL overrides the upstream — for an OpenAI-compatible
	// proxy/gateway, or a mock in e2e tests.
	baseURL := "https://api.openai.com"
	if o := strings.TrimSpace(os.Getenv("PODDLE_OPENAI_BASE_URL")); o != "" {
		baseURL = o
	}
	return broker.Credential{
		Mode:    broker.ModeSubscription,
		Vendor:  "openai",
		Secret:  strings.TrimSpace(string(b)),
		BaseURL: baseURL,
	}, nil
}
