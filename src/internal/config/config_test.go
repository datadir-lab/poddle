package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMerge_ScalarsOverrideListsAppendMapsMerge(t *testing.T) {
	base := Template{Image: "base-img", Size: "weak", Setup: []string{"a"}, Env: map[string]string{"X": "1", "Y": "1"}}
	over := Template{Image: "over-img", Setup: []string{"b"}, Env: map[string]string{"Y": "2", "Z": "3"}}
	m := merge(base, over)

	if m.Image != "over-img" {
		t.Errorf("image = %q, want over-img (override)", m.Image)
	}
	if m.Size != "weak" {
		t.Errorf("size = %q, want weak (kept from base)", m.Size)
	}
	if !reflect.DeepEqual(m.Setup, []string{"a", "b"}) {
		t.Errorf("setup = %v, want [a b] (append)", m.Setup)
	}
	if want := map[string]string{"X": "1", "Y": "2", "Z": "3"}; !reflect.DeepEqual(m.Env, want) {
		t.Errorf("env = %v, want %v (merge, over wins)", m.Env, want)
	}
}

func TestResolve_ExtendsInheritsAndAppends(t *testing.T) {
	c := &Config{templates: map[string]Template{
		"base":     {Image: "node", Setup: []string{"base-setup"}},
		"chromium": {Extends: "base", Setup: []string{"install-chromium"}},
	}}
	got, err := c.Resolve("chromium")
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != "node" {
		t.Errorf("image = %q, want node (inherited)", got.Image)
	}
	if !reflect.DeepEqual(got.Setup, []string{"base-setup", "install-chromium"}) {
		t.Errorf("setup = %v", got.Setup)
	}
}

func TestResolve_MultipleExtends(t *testing.T) {
	c := &Config{templates: map[string]Template{
		"a": {Setup: []string{"a"}, Size: "weak"},
		"b": {Setup: []string{"b"}, Size: "strong"},
		"c": {Extends: []any{"a", "b"}, Setup: []string{"c"}},
	}}
	got, _ := c.Resolve("c")
	if !reflect.DeepEqual(got.Setup, []string{"a", "b", "c"}) {
		t.Errorf("setup = %v", got.Setup)
	}
	if got.Size != "strong" {
		t.Errorf("size = %q, want strong (b is later)", got.Size)
	}
}

func TestResolve_Diamond(t *testing.T) {
	c := &Config{templates: map[string]Template{
		"a": {Setup: []string{"a"}},
		"b": {Extends: "a", Setup: []string{"b"}},
		"c": {Extends: "a", Setup: []string{"c"}},
		"d": {Extends: []any{"b", "c"}, Setup: []string{"d"}},
	}}
	got, err := c.Resolve("d")
	if err != nil {
		t.Fatalf("diamond should resolve, got %v", err)
	}
	if !reflect.DeepEqual(got.Setup, []string{"a", "b", "a", "c", "d"}) {
		t.Errorf("setup = %v", got.Setup)
	}
}

func TestResolve_CycleErrors(t *testing.T) {
	c := &Config{templates: map[string]Template{"a": {Extends: "b"}, "b": {Extends: "a"}}}
	if _, err := c.Resolve("a"); err == nil {
		t.Error("expected a cycle error")
	}
}

func TestResolve_UnknownErrors(t *testing.T) {
	c := &Config{templates: map[string]Template{"a": {Extends: "missing"}}}
	if _, err := c.Resolve("a"); err == nil {
		t.Error("expected unknown-parent error")
	}
	if _, err := c.Resolve("nope"); err == nil {
		t.Error("expected unknown-template error")
	}
}

func TestResolve_EmptyNoDefault(t *testing.T) {
	c := &Config{templates: map[string]Template{}}
	got, err := c.Resolve("")
	if err != nil || got.Image != "" || len(got.Setup) != 0 {
		t.Errorf("empty config should give empty template: %+v, %v", got, err)
	}
}

func TestSetupCommands_InlineThenScripts(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "s.sh")
	if err := os.WriteFile(sp, []byte("echo from-script"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmds, err := Template{Setup: []string{"echo inline"}, Scripts: []string{sp}}.SetupCommands()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"echo inline", "echo from-script"}; !reflect.DeepEqual(cmds, want) {
		t.Errorf("cmds = %v, want %v", cmds, want)
	}
}

func TestLoad_DefaultExtendsNamed_ScriptsAbsolute(t *testing.T) {
	proj := t.TempDir()
	write(t, filepath.Join(proj, ".poddle.toml"), "extends = \"ci\"\nrepo = \"r\"\n")
	write(t, filepath.Join(proj, ".poddle", "ci.toml"), "image = \"node:22\"\nscripts = [\"scripts/ci.sh\"]\n")
	write(t, filepath.Join(proj, ".poddle", "scripts", "ci.sh"), "echo ci")

	cfg, err := Load("", proj)
	if err != nil {
		t.Fatal(err)
	}
	got, err := cfg.Resolve("") // project default
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != "node:22" || got.Repo != "r" {
		t.Errorf("resolved default = %+v", got)
	}
	if len(got.Scripts) != 1 || !filepath.IsAbs(got.Scripts[0]) {
		t.Errorf("script path should be absolute: %v", got.Scripts)
	}
	cmds, err := got.SetupCommands()
	if err != nil || len(cmds) != 1 || cmds[0] != "echo ci" {
		t.Errorf("cmds = %v, %v", cmds, err)
	}
}

func TestLoad_ProjectShadowsUser(t *testing.T) {
	user, proj := t.TempDir(), t.TempDir()
	write(t, filepath.Join(user, "base.toml"), "image = \"user-img\"\n")
	write(t, filepath.Join(proj, ".poddle", "base.toml"), "image = \"proj-img\"\n")

	cfg, err := Load(user, proj)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := cfg.Resolve("base")
	if got.Image != "proj-img" {
		t.Errorf("image = %q, want proj-img (project shadows user)", got.Image)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
