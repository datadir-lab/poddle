// Package google implements the identity.Provider for Google's Gemini models
// (used by the gemini-cli harness). It holds a real Google AI Studio API key and
// hands it to the broker as a ModeGoogleAPIKey credential; the broker injects it
// as x-goog-api-key on the wire, so the key never enters a pod. Only the static
// AI Studio API-key path is brokerable — OAuth ("Login with Google") mints
// expiring tokens and Vertex AI uses application-default credentials, neither of
// which fits poddle's static handle-swap.
package google

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

const tokenFile = "google-token"

// stdin is injected for testing; in production it is os.Stdin.
var stdin io.Reader = os.Stdin

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Name() string { return "google" }

func (p *Provider) tokenPath(id identity.Identity) string {
	return filepath.Join(id.Dir(), tokenFile)
}

// Authenticate captures the API key non-interactively: GEMINI_API_KEY, then
// GOOGLE_API_KEY (the two names gemini-cli itself accepts), else one line from
// stdin. No browser, no TTY dependency (agent/CI shells have no pinentry).
func (p *Provider) Authenticate(id identity.Identity) error {
	token := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	}
	if token == "" {
		fmt.Fprint(os.Stderr, "Paste a Google AI Studio API key (AIza...): ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		token = strings.TrimSpace(line)
	}
	if token == "" {
		return fmt.Errorf("no Google API key provided (set GEMINI_API_KEY or GOOGLE_API_KEY)")
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

// Credential returns the stored key as a ModeGoogleAPIKey credential. BaseURL is
// the origin with NO path — gemini-cli's @google/genai SDK appends the
// /v1beta/models/... path itself, and that path rides through to the upstream.
func (p *Provider) Credential(id identity.Identity) (broker.Credential, error) {
	b, err := os.ReadFile(p.tokenPath(id))
	if err != nil {
		return broker.Credential{}, fmt.Errorf("read token: %w", err)
	}
	// PODDLE_GOOGLE_BASE_URL overrides the upstream — for a compatible proxy or a
	// mock in e2e tests.
	baseURL := "https://generativelanguage.googleapis.com"
	if o := strings.TrimSpace(os.Getenv("PODDLE_GOOGLE_BASE_URL")); o != "" {
		baseURL = o
	}
	return broker.Credential{
		Mode:    broker.ModeGoogleAPIKey,
		Vendor:  "google",
		Secret:  strings.TrimSpace(string(b)),
		BaseURL: baseURL,
	}, nil
}
