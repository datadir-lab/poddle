package connector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// Connection is a named, owner-scoped instance of a connector — one user's
// token for one service. Only the token is secret (stored 0600 in dir).
type Connection struct {
	Name      string // from the store directory
	Connector string // definition name (forgejo, woodpecker, …)
	BaseURL   string
	User      string
	Owner     string // the tenant this credential belongs to ("local" in Phase 1)
	dir       string
}

func (c Connection) tokenPath() string { return filepath.Join(c.dir, c.Connector+"-token") }

// oauthPath is where a connection's OAuth material (if any) is sealed,
// alongside its meta.toml.
func (c Connection) oauthPath() string { return filepath.Join(c.dir, "oauth.json") }

// OAuthMaterial is the OAuth 2.1 token set for a connection that authenticates
// via an OAuth flow (e.g. a remote MCP server) instead of a static bearer
// token. The gateway uses RefreshToken/TokenEndpoint/ClientID(+Secret) to
// refresh AccessToken before ExpiresAt.
type OAuthMaterial struct {
	AccessToken   string `json:"access"`
	RefreshToken  string `json:"refresh"`
	TokenEndpoint string `json:"token_endpoint"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	Scope         string `json:"scope"`

	ExpiresAt time.Time `json:"expires_at"`
}

type connMeta struct {
	Connector string `toml:"connector"`
	BaseURL   string `toml:"base_url"`
	User      string `toml:"user"`
	Owner     string `toml:"owner"`
}

// Store persists connections under a base directory (one dir per name).
type Store struct{ base string }

// NewStore returns a Store rooted at base.
func NewStore(base string) *Store { return &Store{base: base} }

// DefaultBase is ~/.config/poddle/connections.
func DefaultBase() string {
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "poddle", "connections")
}

// Create stores a connection and its token. Owner "" defaults to "local".
func (s *Store) Create(name, connector, baseURL, user, token, owner string) (Connection, error) {
	if owner == "" {
		owner = "local"
	}
	dir := filepath.Join(s.base, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Connection{}, err
	}
	b, err := toml.Marshal(connMeta{Connector: connector, BaseURL: baseURL, User: user, Owner: owner})
	if err != nil {
		return Connection{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.toml"), b, 0o600); err != nil {
		return Connection{}, err
	}
	conn := Connection{Name: name, Connector: connector, BaseURL: baseURL, User: user, Owner: owner, dir: dir}
	if err := os.WriteFile(conn.tokenPath(), []byte(token), 0o600); err != nil {
		return Connection{}, err
	}
	return conn, nil
}

// Get loads a connection by name.
func (s *Store) Get(name string) (Connection, error) {
	dir := filepath.Join(s.base, name)
	b, err := os.ReadFile(filepath.Join(dir, "meta.toml"))
	if err != nil {
		return Connection{}, err
	}
	var m connMeta
	if err := toml.Unmarshal(b, &m); err != nil {
		return Connection{}, err
	}
	return Connection{Name: name, Connector: m.Connector, BaseURL: m.BaseURL, User: m.User, Owner: m.Owner, dir: dir}, nil
}

// List returns every stored connection (skipping malformed entries).
func (s *Store) List() ([]Connection, error) {
	entries, err := os.ReadDir(s.base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Connection
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if c, err := s.Get(e.Name()); err == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

// Remove deletes a connection and its token.
func (s *Store) Remove(name string) error {
	return os.RemoveAll(filepath.Join(s.base, name))
}

// SaveOAuth persists a connection's OAuth material as a sealed oauth.json
// (0600) beside its meta.toml.
func (s *Store) SaveOAuth(name string, m OAuthMaterial) error {
	conn := Connection{Name: name, dir: filepath.Join(s.base, name)}
	//nolint:gosec // G117: sealing OAuth tokens to the host's 0600 oauth.json is the design; the secretless invariant keeps them off the pod, not off the host connection store.
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(conn.oauthPath(), b, 0o600)
}

// LoadOAuth loads a connection's OAuth material. ok is false (with a nil
// error) when the connection has no oauth.json — i.e. it authenticates with a
// static token instead.
func (s *Store) LoadOAuth(name string) (OAuthMaterial, bool, error) {
	conn := Connection{Name: name, dir: filepath.Join(s.base, name)}
	b, err := os.ReadFile(conn.oauthPath())
	if err != nil {
		if os.IsNotExist(err) {
			return OAuthMaterial{}, false, nil
		}
		return OAuthMaterial{}, false, err
	}
	var m OAuthMaterial
	if err := json.Unmarshal(b, &m); err != nil {
		return OAuthMaterial{}, false, err
	}
	return m, true, nil
}
