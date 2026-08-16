// Package secure enforces poddle's "keep the user's own secrets safe" rules:
// which host paths may never enter a pod (block_paths), scanning mounts for
// credential files, and redacting secrets from broker egress.
package secure

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

// defaultBlocked is the always-on deny-list: well-known host secret stores plus
// poddle's own config dir, so a careless mount can't exfiltrate stored tokens.
func defaultBlocked() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		for _, p := range []string{
			".ssh", ".aws", ".gnupg", ".kube", ".netrc",
			filepath.Join(".config", "gcloud"),
			filepath.Join(".docker", "config.json"),
		} {
			out = append(out, filepath.Join(home, p))
		}
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		out = append(out, filepath.Join(cfg, "poddle")) // poddle's own secret store
	}
	return out
}

// CheckMounts rejects any mount whose host path overlaps a blocked path — in
// either direction: a mount that is or contains a blocked path, or sits under
// one. extra extends the always-on default deny-list (e.g. a template's
// block_paths). It fails closed on the first violation.
func CheckMounts(mounts []sandbox.Mount, extra []string) error {
	var blocked []string
	for _, b := range append(defaultBlocked(), extra...) {
		blocked = append(blocked, resolve(b))
	}
	for _, m := range mounts {
		host := resolve(m.Host)
		for i, b := range blocked {
			if overlaps(host, b) {
				// Report the original (un-resolved) blocked entry for clarity.
				orig := append(defaultBlocked(), extra...)[i]
				return fmt.Errorf("mount %q would expose blocked path %q; refusing (block_paths)", m.Host, orig)
			}
		}
	}
	return nil
}

// Finding is a credential-looking file discovered inside a mount.
type Finding struct {
	Path string // absolute host path
	Rule string // why it matched (e.g. ".env", "private key")
}

// scanMaxDepth bounds how deep ScanMounts descends into a mounted directory.
const scanMaxDepth = 2

// ScanMounts shallow-walks each mounted directory for files that look like
// credentials. It is advisory — the caller decides warn vs block. Noisy build
// dirs are skipped and the walk is depth-bounded to stay cheap on big repos.
func ScanMounts(mounts []sandbox.Mount) []Finding {
	var out []Finding
	for _, m := range mounts {
		host := resolve(m.Host)
		info, err := os.Stat(host)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if rule := credRule(filepath.Base(host)); rule != "" {
				out = append(out, Finding{Path: host, Rule: rule})
			}
			continue
		}
		_ = filepath.WalkDir(host, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if p == host {
					return nil
				}
				rel, _ := filepath.Rel(host, p)
				depth := strings.Count(rel, string(filepath.Separator)) + 1
				if depth >= scanMaxDepth || skipDir(d.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if rule := credRule(d.Name()); rule != "" {
				out = append(out, Finding{Path: p, Rule: rule})
			}
			return nil
		})
	}
	return out
}

// skipDir reports directories not worth scanning for secrets.
func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "venv", "dist", "build", "target":
		return true
	}
	return false
}

// credRule returns a short reason if name looks like a credential file, else "".
func credRule(name string) string {
	lower := strings.ToLower(name)
	switch lower {
	case ".env", ".git-credentials", ".npmrc", ".pypirc", ".netrc", ".dockercfg",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519":
		return name
	}
	if strings.HasPrefix(lower, ".env.") {
		switch lower {
		case ".env.example", ".env.sample", ".env.template", ".env.dist":
			return ""
		}
		return name
	}
	if strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") {
		return "private key"
	}
	if strings.HasPrefix(lower, "service-account") && strings.HasSuffix(lower, ".json") {
		return "service account key"
	}
	return ""
}

// resolve makes a path absolute, expands a leading ~, and resolves symlinks on
// the longest existing ancestor (so non-existent paths still normalize).
func resolve(p string) string {
	p = expandTilde(p)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p // reached the root without resolving anything
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// expandTilde replaces a leading ~ (with / or \ separator, or bare ~) with the
// user's home directory.
func expandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	rest := strings.TrimPrefix(p, "~")
	rest = strings.TrimPrefix(rest, "/")
	rest = strings.TrimPrefix(rest, `\`)
	return filepath.Join(home, rest)
}

// overlaps reports whether a and b are the same path or one contains the other.
func overlaps(a, b string) bool {
	return a == b || isUnder(a, b) || isUnder(b, a)
}

// isUnder reports whether child sits within parent.
func isUnder(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false // different volumes, etc.
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
