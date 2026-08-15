//go:build e2e

package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const module = "github.com/datadir-lab/poddle"

// buildBinary compiles the poddle binary into a temp dir and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "poddle")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, module+"/src/cli").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestSmoke_RootHelp(t *testing.T) {
	out, err := exec.Command(buildBinary(t), "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("run --help: %v\n%s", err, out)
	}
	for _, w := range []string{"poddle", "ls"} {
		if !strings.Contains(string(out), w) {
			t.Errorf("--help missing %q in:\n%s", w, out)
		}
	}
}

func TestSmoke_LsHelp(t *testing.T) {
	out, err := exec.Command(buildBinary(t), "ls", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("run ls --help: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "List sandboxes") {
		t.Errorf("ls --help missing description:\n%s", out)
	}
}
