package openai

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/identity"
)

// idIn makes an Identity rooted at a temp dir for the provider to write into.
func idIn(t *testing.T) identity.Identity {
	t.Helper()
	st := identity.NewStore(t.TempDir())
	id, err := st.Create("work", "openai")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestProvider_Name(t *testing.T) {
	if New().Name() != "openai" {
		t.Errorf("Name = %q, want openai", New().Name())
	}
}

func TestAuthenticate_APIKeyFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-123")
	id := idIn(t)
	p := New()
	if err := p.Authenticate(id); err != nil {
		t.Fatal(err)
	}
	tok, _ := os.ReadFile(filepath.Join(id.Dir(), "openai-token"))
	if string(tok) != "sk-test-123" {
		t.Errorf("token = %q", tok)
	}
	ok, err := p.IsAuthenticated(id)
	if err != nil || !ok {
		t.Errorf("IsAuthenticated = %v, %v", ok, err)
	}
}

func TestCredential_BearerAndBaseURL(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-abc")
	t.Setenv("PODDLE_OPENAI_BASE_URL", "")
	id := idIn(t)
	p := New()
	if err := p.Authenticate(id); err != nil {
		t.Fatal(err)
	}
	c, err := p.Credential(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != broker.ModeSubscription || c.Vendor != "openai" || c.Secret != "sk-abc" {
		t.Errorf("cred = %+v", c)
	}
	if c.BaseURL != "https://api.openai.com" { // NO /v1 path — harness adds /v1
		t.Errorf("BaseURL = %q, want https://api.openai.com", c.BaseURL)
	}
}

func TestCredential_BaseURLOverride(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-abc")
	t.Setenv("PODDLE_OPENAI_BASE_URL", "http://host.containers.internal:9000")
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
