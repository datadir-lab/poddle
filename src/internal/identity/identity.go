// Package identity manages named coding-agent logins ("identities"), stored on
// the client (where you attach from) — never only inside poddle. Each identity
// belongs to a provider (the auth vendor: anthropic, openai, local); providers
// are vertically sliced and implement Provider.
package identity

import (
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// Identity is a named credential belonging to a provider. Dir is where the
// provider keeps this identity's credential material on the client.
type Identity struct {
	Name     string
	Provider string
	dir      string
}

// Dir is the on-disk directory a provider uses for this identity's creds.
func (i Identity) Dir() string { return i.dir }

// Store persists identities under a base directory.
type Store struct{ base string }

// NewStore returns a Store rooted at base.
func NewStore(base string) *Store { return &Store{base: base} }

// DefaultBase is ~/.config/poddle/identities (XDG/AppData-correct).
func DefaultBase() string {
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "poddle", "identities")
}

type meta struct {
	Name     string `toml:"name"`
	Provider string `toml:"provider"`
}

// Create makes (or updates) an identity's directory and metadata.
func (s *Store) Create(name, provider string) (Identity, error) {
	dir := filepath.Join(s.base, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Identity{}, err
	}
	b, err := toml.Marshal(meta{Name: name, Provider: provider})
	if err != nil {
		return Identity{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.toml"), b, 0o600); err != nil {
		return Identity{}, err
	}
	return Identity{Name: name, Provider: provider, dir: dir}, nil
}

// Get loads a single identity by name.
func (s *Store) Get(name string) (Identity, error) {
	dir := filepath.Join(s.base, name)
	b, err := os.ReadFile(filepath.Join(dir, "meta.toml"))
	if err != nil {
		return Identity{}, err
	}
	var m meta
	if err := toml.Unmarshal(b, &m); err != nil {
		return Identity{}, err
	}
	return Identity{Name: m.Name, Provider: m.Provider, dir: dir}, nil
}

// List returns every stored identity (skipping malformed entries).
func (s *Store) List() ([]Identity, error) {
	entries, err := os.ReadDir(s.base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Identity
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, err := s.Get(e.Name())
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// Remove deletes an identity and its credentials.
func (s *Store) Remove(name string) error {
	return os.RemoveAll(filepath.Join(s.base, name))
}
