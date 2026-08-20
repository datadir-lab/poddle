package up

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/config"
)

// TestUp_MountBlockRefused: a template mount that overlaps a block_paths entry is
// refused before the pod is created.
func TestUp_MountBlockRefused(t *testing.T) {
	tpl := config.Template{
		BlockPaths: []string{"/etc/poddle-secret"},
		Mounts:     []config.Mount{{Host: "/etc/poddle-secret/creds", Container: "/creds"}},
	}
	c := NewCmd(&app.App{Engine: &fakeCreator{}, Harnesses: testHarnesses(), Templates: fakeTemplates{tpl: tpl}}, stubBroker{})
	c.SilenceUsage = true
	c.SetArgs([]string{"box"})
	if err := c.Execute(); err == nil {
		t.Error("expected a blocked-mount refusal")
	}
}

// TestUp_SecretScanBlockRefused: with secret_scan=block, a credential file inside
// a mount aborts the run.
func TestUp_SecretScanBlockRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tpl := config.Template{
		SecretScan: "block",
		Mounts:     []config.Mount{{Host: dir, Container: "/work"}},
	}
	c := NewCmd(&app.App{Engine: &fakeCreator{}, Harnesses: testHarnesses(), Templates: fakeTemplates{tpl: tpl}}, stubBroker{})
	c.SilenceUsage = true
	c.SetArgs([]string{"box"})
	if err := c.Execute(); err == nil {
		t.Error("expected a secret_scan=block refusal")
	}
}

// TestUp_SecretScanWarnProceeds: with secret_scan=warn (the default), the same
// credential file only warns to stderr and the pod is still created.
func TestUp_SecretScanWarnProceeds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tpl := config.Template{
		SecretScan: "warn",
		Mounts:     []config.Mount{{Host: dir, Container: "/work"}},
	}
	f := &fakeCreator{}
	c := NewCmd(&app.App{Engine: f, Harnesses: testHarnesses(), Templates: fakeTemplates{tpl: tpl}}, stubBroker{})
	var errBuf bytes.Buffer
	c.SetErr(&errBuf)
	c.SetArgs([]string{"box"})
	if err := c.Execute(); err != nil {
		t.Fatalf("warn should proceed, got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "credential files") {
		t.Errorf("expected a stderr warning about credential files, got: %q", errBuf.String())
	}
	if f.spec.Name != "box" {
		t.Error("pod should have been created despite the warning")
	}
}
