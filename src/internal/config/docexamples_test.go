package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// TestDocExampleTemplates lints the template examples the docs site renders
// (src/web/site/src/data/templates/*.toml) against the real parser, so a
// published example can never drift into something the CLI would reject.
// Runs in CI (task test) and on every `task web-docs`.
func TestDocExampleTemplates(t *testing.T) {
	dir := filepath.Join("..", "..", "web", "site", "src", "data", "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read example templates dir %s: %v", dir, err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".toml"))

		// Strict decode: reject unknown keys (typos) the loose loader would ignore.
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		dec := toml.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		var tmpl Template
		if err := dec.Decode(&tmpl); err != nil {
			t.Errorf("%s: not a valid template: %v", e.Name(), err)
		}
	}
	if len(names) == 0 {
		t.Fatal("no example templates found to lint")
	}

	// Full load + resolve via the real parser — validates extends chains / cycles.
	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("load example templates: %v", err)
	}
	for _, n := range names {
		if _, err := cfg.Resolve(n); err != nil {
			t.Errorf("resolve %q: %v", n, err)
		}
	}
}
