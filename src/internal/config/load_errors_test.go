package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_BadTemplateFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.toml"), []byte("this = = invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, ""); err == nil {
		t.Error("expected Load to fail on a malformed template file")
	}
}

func TestLoad_BadDefaultTemplateErrors(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".poddle.toml"), []byte("bad = = toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(t.TempDir(), proj); err == nil {
		t.Error("expected Load to fail on a malformed .poddle.toml")
	}
}

func TestLoad_ResolvesRelativeScriptsAndMounts(t *testing.T) {
	proj := t.TempDir()
	doc := "scripts = [\"setup.sh\"]\n[[mounts]]\nhost = \"data\"\ncontainer = \"/data\"\n"
	if err := os.WriteFile(filepath.Join(proj, ".poddle.toml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(t.TempDir(), proj)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tpl, err := cfg.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(tpl.Scripts) == 0 || !filepath.IsAbs(tpl.Scripts[0]) {
		t.Errorf("relative script path was not made absolute: %v", tpl.Scripts)
	}
	if len(tpl.Mounts) == 0 || !filepath.IsAbs(tpl.Mounts[0].Host) {
		t.Errorf("relative mount host was not made absolute: %v", tpl.Mounts)
	}
}

func TestLoad_MissingDirsAreEmpty(t *testing.T) {
	// Neither the user templates dir nor the project dir exist: Load succeeds
	// with no templates rather than erroring.
	cfg, err := Load(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "gone"))
	if err != nil {
		t.Fatalf("Load with missing dirs should not error: %v", err)
	}
	if _, err := cfg.Resolve(""); err != nil {
		t.Errorf("Resolve of empty config: %v", err)
	}
}
