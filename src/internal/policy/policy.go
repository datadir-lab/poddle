// Package policy is poddle's governance policy model: named, owner-scoped rules
// (~/.config/poddle/policies/<name>.toml) that decide what a pod may do. The
// broker consults a pod's policy on each request and records the decision into
// the audit spine. A nil/empty policy allows all — policy is opt-in.
package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Policy is a set of egress/access rules referenced by a pod.
type Policy struct {
	Name           string              `toml:"-"`
	AllowUpstreams []string            `toml:"allow_upstreams"` // default-deny when non-empty; ".x" = any subdomain
	DenyUpstreams  []string            `toml:"deny_upstreams"`  // always denied (wins over allow)
	Methods        map[string][]string `toml:"methods"`         // per-host allowed HTTP methods
	Egress         string              `toml:"egress"`          // redact (default) | block | off
}

// Decide evaluates one request against the policy. Order: the deny-list wins,
// then the allow-list (default-deny when it is non-empty), then per-host method
// rules; otherwise allow. A nil policy allows everything.
func (p *Policy) Decide(host, method string) (allow bool, reason string) {
	if p == nil {
		return true, ""
	}
	if matchHost(host, p.DenyUpstreams) {
		return false, "denied upstream: " + host
	}
	if len(p.AllowUpstreams) > 0 && !matchHost(host, p.AllowUpstreams) {
		return false, "upstream not allow-listed: " + host
	}
	// Method rules apply to plain HTTP only; a CONNECT tunnel's real method is
	// encrypted, so it is governed by the destination rules alone.
	if allowed, ok := p.methodsFor(host); ok && method != "CONNECT" && !containsFold(allowed, method) {
		return false, "method " + method + " not allowed for " + host
	}
	return true, ""
}

// methodsFor returns the allowed methods for host (exact key, then a ".suffix"
// key), and whether a rule applies.
func (p *Policy) methodsFor(host string) ([]string, bool) {
	if m, ok := p.Methods[host]; ok {
		return m, true
	}
	for k, m := range p.Methods {
		if strings.HasPrefix(k, ".") && (strings.HasSuffix(host, k) || host == k[1:]) {
			return m, true
		}
	}
	return nil, false
}

// matchHost reports whether host matches any pattern: exact, or a ".suffix"
// pattern matching that domain and any subdomain.
func matchHost(host string, patterns []string) bool {
	for _, p := range patterns {
		if p == host {
			return true
		}
		if strings.HasPrefix(p, ".") && (strings.HasSuffix(host, p) || host == p[1:]) {
			return true
		}
	}
	return false
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// Store reads and writes named policies. A file-backed store serves the
// self-hosted tiers (project + global dirs); a Postgres-backed store will serve
// the multi-tenant cloud tier — both behind this one interface, so the UI and
// enforcement path never change.
type Store interface {
	Get(name string) (*Policy, error)
	List() ([]string, error)
	Put(p *Policy) error
	Delete(name string) error
}

// FileStore is a directory of <name>.toml policies.
type FileStore struct{ dir string }

// NewFileStore returns a file-backed store rooted at dir.
func NewFileStore(dir string) *FileStore { return &FileStore{dir: dir} }

// DefaultDir is ~/.config/poddle/policies (XDG_CONFIG_HOME honored via UserConfigDir).
func DefaultDir() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		cfg = "."
	}
	return filepath.Join(cfg, "poddle", "policies")
}

// ProjectDir is a repo's versioned policy dir: <cwd>/poddle/policies.
func ProjectDir(cwd string) string { return filepath.Join(cwd, "poddle", "policies") }

// Get loads and parses the named policy (<name>.toml).
func (s *FileStore) Get(name string) (*Policy, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, name+".toml"))
	if err != nil {
		return nil, fmt.Errorf("policy %q: %w", name, err)
	}
	var p Policy
	if err := toml.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("policy %q: %w", name, err)
	}
	p.Name = name
	return &p, nil
}

// List returns the names of the policies in the dir (sorted). A missing dir is
// empty, not an error.
func (s *FileStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
		}
	}
	sort.Strings(names)
	return names, nil
}

// Put writes a policy as <name>.toml (creating the dir if needed).
func (s *FileStore) Put(p *Policy) error {
	if p.Name == "" {
		return fmt.Errorf("policy has no name")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	b, err := toml.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, p.Name+".toml"), b, 0o600)
}

// Delete removes the named policy file.
func (s *FileStore) Delete(name string) error {
	err := os.Remove(filepath.Join(s.dir, name+".toml"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Layered reads policies from ordered sources (first match wins — project
// shadows global) and writes through Writer. It is the self-hosted composition:
// Layered{Sources: {project, global}, Writer: global}.
type Layered struct {
	Sources []Store
	Writer  Store
}

func (l Layered) Get(name string) (*Policy, error) {
	var lastErr error
	for _, s := range l.Sources {
		p, err := s.Get(name)
		if err == nil {
			return p, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("policy %q: not found", name)
	}
	return nil, lastErr
}

func (l Layered) List() ([]string, error) {
	seen := map[string]bool{}
	var names []string
	for _, s := range l.Sources {
		ns, err := s.List()
		if err != nil {
			return nil, err
		}
		for _, n := range ns {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

func (l Layered) Put(p *Policy) error      { return l.Writer.Put(p) }
func (l Layered) Delete(name string) error { return l.Writer.Delete(name) }
