package connector

import (
	"os"
	"path/filepath"

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
