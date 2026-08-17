// Package config loads poddle templates — reusable pod blueprints (image, size,
// harness, identity, repo, env, mounts, setup, scripts) defined in TOML. One
// file is one template, named by its filename. Templates compose via `extends`
// (scalars override, lists append, maps merge). A project's root .poddle.toml
// is the auto-applied default; .poddle/<name>.toml and
// ~/.config/poddle/templates/<name>.toml are named (selected with --template).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Mount is a host→container bind mount in a template.
type Mount struct {
	Host      string `toml:"host"`
	Container string `toml:"container"`
	ReadOnly  bool   `toml:"ro"`
}

// Template is a pod blueprint parsed from one TOML file.
type Template struct {
	Extends    any               `toml:"extends"` // string or []string
	Image      string            `toml:"image"`
	Size       string            `toml:"size"`
	Harness    string            `toml:"harness"`
	Identity   string            `toml:"identity"`
	Repo       string            `toml:"repo"`
	Env        map[string]string `toml:"env"`
	Mounts     []Mount           `toml:"mounts"`
	Setup      []string          `toml:"setup"`       // inline commands
	Scripts    []string          `toml:"scripts"`     // script files (absolute after load), read + run
	Connectors []string          `toml:"connectors"`  // connection names to broker into the pod
	BlockPaths []string          `toml:"block_paths"` // host paths that must never enter the pod
	SecretScan string            `toml:"secret_scan"` // credential-file scan on mounts: off | warn (default) | block
	Egress     string            `toml:"egress"`      // broker egress redaction: redact (default) | block | off
	BeforeTask string            `toml:"before_task"` // resize to this size before a `poddle task` run
	AfterTask  string            `toml:"after_task"`  // resize to this size after a kept task run
	Autoscale  bool              `toml:"autoscale"`   // opt in to the daemon's reactive memory-grow autoscaler
}

// extendsList normalizes the string-or-list `extends` into a slice.
func (t Template) extendsList() []string {
	switch x := t.Extends.(type) {
	case string:
		if x == "" {
			return nil
		}
		return []string{x}
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// SetupCommands returns the pod setup commands: inline Setup first, then the
// contents of each referenced script (read from disk), in order.
func (t Template) SetupCommands() ([]string, error) {
	cmds := append([]string{}, t.Setup...)
	for _, path := range t.Scripts {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("template script %q: %w", path, err)
		}
		cmds = append(cmds, string(b))
	}
	return cmds, nil
}

// Resolver resolves a template name to a fully-merged Template. name == ""
// means the project default.
type Resolver interface {
	Resolve(name string) (Template, error)
}

// DirResolver loads templates lazily from a user templates dir and a project
// dir on each Resolve — so a malformed project file only fails the command that
// needs a template, not the whole CLI.
type DirResolver struct {
	UserDir    string
	ProjectDir string
}

func (r DirResolver) Resolve(name string) (Template, error) {
	cfg, err := Load(r.UserDir, r.ProjectDir)
	if err != nil {
		return Template{}, err
	}
	return cfg.Resolve(name)
}

// Config is a resolved namespace of named templates plus an optional project
// default (the root .poddle.toml).
type Config struct {
	templates map[string]Template
	def       *Template
}

// Resolve returns the fully-merged template for name. name == "" resolves the
// project default (empty template if there is none); a non-empty name resolves
// that named template. `extends` chains are flattened; cycles error.
func (c *Config) Resolve(name string) (Template, error) {
	if name == "" {
		if c.def == nil {
			return Template{}, nil
		}
		return resolve(*c.def, c.templates, map[string]bool{})
	}
	t, ok := c.templates[name]
	if !ok {
		return Template{}, fmt.Errorf("unknown template %q", name)
	}
	return resolve(t, c.templates, map[string]bool{name: true})
}

// resolve flattens t's extends chain against the template namespace. seen holds
// the names in the current chain (per-branch) for cycle detection.
func resolve(t Template, ts map[string]Template, seen map[string]bool) (Template, error) {
	acc := Template{}
	for _, parent := range t.extendsList() {
		if seen[parent] {
			return Template{}, fmt.Errorf("template cycle via %q", parent)
		}
		pt, ok := ts[parent]
		if !ok {
			return Template{}, fmt.Errorf("unknown parent template %q", parent)
		}
		branch := copySet(seen)
		branch[parent] = true
		r, err := resolve(pt, ts, branch)
		if err != nil {
			return Template{}, err
		}
		acc = merge(acc, r)
	}
	return merge(acc, t), nil
}

// merge layers over on top of base: scalars override (non-empty over wins),
// lists append (base then over), maps merge (over keys win).
func merge(base, over Template) Template {
	m := Template{
		Image:      pick(over.Image, base.Image),
		Size:       pick(over.Size, base.Size),
		Harness:    pick(over.Harness, base.Harness),
		Identity:   pick(over.Identity, base.Identity),
		Repo:       pick(over.Repo, base.Repo),
		SecretScan: pick(over.SecretScan, base.SecretScan),
		Egress:     pick(over.Egress, base.Egress),
		BeforeTask: pick(over.BeforeTask, base.BeforeTask),
		AfterTask:  pick(over.AfterTask, base.AfterTask),
		Autoscale:  over.Autoscale || base.Autoscale,
		Setup:      concat(base.Setup, over.Setup),
		Scripts:    concat(base.Scripts, over.Scripts),
		Connectors: concat(base.Connectors, over.Connectors),
		BlockPaths: concat(base.BlockPaths, over.BlockPaths),
	}
	m.Mounts = append(append([]Mount{}, base.Mounts...), over.Mounts...)
	if len(m.Mounts) == 0 {
		m.Mounts = nil
	}
	if len(base.Env) > 0 || len(over.Env) > 0 {
		env := map[string]string{}
		for k, v := range base.Env {
			env[k] = v
		}
		for k, v := range over.Env {
			env[k] = v
		}
		m.Env = env
	}
	return m
}

func pick(over, base string) string {
	if over != "" {
		return over
	}
	return base
}

func concat(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	return append(append([]string{}, a...), b...)
}

func copySet(s map[string]bool) map[string]bool {
	c := make(map[string]bool, len(s))
	for k := range s {
		c[k] = true
	}
	return c
}

// Load gathers named templates from the user templates dir and the project's
// .poddle/ dir (project shadows user), plus the project default (root
// .poddle.toml). Missing dirs/files are not errors.
func Load(userTemplatesDir, projectDir string) (*Config, error) {
	ts := map[string]Template{}
	if err := loadDir(userTemplatesDir, ts); err != nil {
		return nil, err
	}
	if err := loadDir(filepath.Join(projectDir, ".poddle"), ts); err != nil {
		return nil, err
	}
	cfg := &Config{templates: ts}
	defPath := filepath.Join(projectDir, ".poddle.toml")
	if fileExists(defPath) {
		t, err := loadTemplateFile(defPath)
		if err != nil {
			return nil, err
		}
		cfg.def = &t
	}
	return cfg, nil
}

// loadDir loads every top-level *.toml in dir as a template named by filename.
func loadDir(dir string, into map[string]Template) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		t, err := loadTemplateFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		into[strings.TrimSuffix(e.Name(), ".toml")] = t
	}
	return nil
}

// loadTemplateFile parses one template and resolves its script/mount paths to
// absolute (relative to the file's own directory).
func loadTemplateFile(path string) (Template, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Template{}, err
	}
	var t Template
	if err := toml.Unmarshal(b, &t); err != nil {
		return Template{}, fmt.Errorf("parse %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	for i, s := range t.Scripts {
		if !filepath.IsAbs(s) {
			t.Scripts[i] = filepath.Join(dir, s)
		}
	}
	for i, m := range t.Mounts {
		if m.Host != "" && !filepath.IsAbs(m.Host) {
			t.Mounts[i].Host = filepath.Join(dir, m.Host)
		}
	}
	return t, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
