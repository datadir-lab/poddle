package google

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/identity"
)

// Ensure Provider implements identity.Provider.
var _ identity.Provider = (*Provider)(nil)

// idIn makes an Identity rooted at a temp dir for the provider to write into.
func idIn(t *testing.T) identity.Identity {
	t.Helper()
	st := identity.NewStore(t.TempDir())
	id, err := st.Create("work", "google")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestProvider_Name(t *testing.T) {
	if New().Name() != "google" {
		t.Errorf("Name = %q, want google", New().Name())
	}
}

func TestAuthenticate_APIKeyFromGeminiEnv(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "AIza-test-123")
	t.Setenv("GOOGLE_API_KEY", "")
	id := idIn(t)
	p := New()
	if err := p.Authenticate(id); err != nil {
		t.Fatal(err)
	}
	tok, _ := os.ReadFile(filepath.Join(id.Dir(), "google-token"))
	if string(tok) != "AIza-test-123" {
		t.Errorf("token = %q", tok)
	}
	ok, err := p.IsAuthenticated(id)
	if err != nil || !ok {
		t.Errorf("IsAuthenticated = %v, %v", ok, err)
	}
}

func TestAuthenticate_FallsBackToGoogleEnv(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "AIza-from-google")
	id := idIn(t)
	if err := New().Authenticate(id); err != nil {
		t.Fatal(err)
	}
	tok, _ := os.ReadFile(filepath.Join(id.Dir(), "google-token"))
	if string(tok) != "AIza-from-google" {
		t.Errorf("token = %q, want AIza-from-google", tok)
	}
}

func TestCredential_GoogleAPIKeyModeAndBaseURL(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "AIza-abc")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("PODDLE_GOOGLE_BASE_URL", "")
	id := idIn(t)
	p := New()
	if err := p.Authenticate(id); err != nil {
		t.Fatal(err)
	}
	c, err := p.Credential(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != broker.ModeGoogleAPIKey || c.Vendor != "google" || c.Secret != "AIza-abc" {
		t.Errorf("cred = %+v", c)
	}
	if c.BaseURL != "https://generativelanguage.googleapis.com" { // origin only — SDK adds /v1beta
		t.Errorf("BaseURL = %q, want https://generativelanguage.googleapis.com", c.BaseURL)
	}
}

func TestCredential_BaseURLOverride(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "AIza-abc")
	t.Setenv("PODDLE_GOOGLE_BASE_URL", "http://host.containers.internal:9000")
	id := idIn(t)
	p := New()
	_ = p.Authenticate(id)
	c, _ := p.Credential(id)
	if c.BaseURL != "http://host.containers.internal:9000" {
		t.Errorf("override BaseURL = %q", c.BaseURL)
	}
}

func TestIsAuthenticated_FalseWhenAbsent(t *testing.T) {
	ok, err := New().IsAuthenticated(idIn(t))
	if err != nil || ok {
		t.Errorf("want false,nil; got %v,%v", ok, err)
	}
}

func TestCredential_ErrsWhenNoToken(t *testing.T) {
	if _, err := New().Credential(idIn(t)); err == nil {
		t.Error("want error when no token stored")
	}
}

func TestAuthenticate_StdinFallback(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	old := stdin
	defer func() { stdin = old }()
	stdin = strings.NewReader("AIza-from-stdin\n")
	id := idIn(t)
	if err := New().Authenticate(id); err != nil {
		t.Fatal(err)
	}
	tok, _ := os.ReadFile(filepath.Join(id.Dir(), "google-token"))
	if string(tok) != "AIza-from-stdin" {
		t.Errorf("token = %q, want AIza-from-stdin", string(tok))
	}
}

func TestAuthenticate_ErrsWhenNoKeyAnywhere(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	old := stdin
	defer func() { stdin = old }()
	stdin = strings.NewReader("")
	if err := New().Authenticate(idIn(t)); err == nil {
		t.Error("want error when no key provided")
	}
}
