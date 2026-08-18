// Package policy is poddle's governance policy model: named, owner-scoped rules
// (~/.config/poddle/policies/<name>.toml) that decide what a pod may do. The
// broker consults a pod's policy on each request and records the decision into
// the audit spine. A nil/empty policy allows all — policy is opt-in.
package policy

import (
	"fmt"
	"os"
	"path/filepath"
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
	if allowed, ok := p.methodsFor(host); ok && !containsFold(allowed, method) {
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

// Store loads named policies from a directory.
type Store struct{ dir string }

// NewStore returns a policy store rooted at dir.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// DefaultDir is ~/.config/poddle/policies (XDG_CONFIG_HOME honored via UserConfigDir).
func DefaultDir() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		cfg = "."
	}
	return filepath.Join(cfg, "poddle", "policies")
}

// Get loads and parses the named policy (<name>.toml).
func (s *Store) Get(name string) (*Policy, error) {
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
