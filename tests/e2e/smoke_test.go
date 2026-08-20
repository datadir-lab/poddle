//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const module = "github.com/datadir-lab/poddle"

// buildBinary compiles the poddle binary into a temp dir and returns its path.
//
// When GOCOVERDIR is set (the nightly e2e-coverage job), the binary is built
// with coverage instrumentation spanning all of src/. Every invocation — and
// the daemon it spawns, which inherits the env — then writes coverage data to
// $GOCOVERDIR on exit, so the e2e suites' coverage can be reported to Codecov.
// With GOCOVERDIR unset (local `task e2e`, the default), the build is unchanged.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "poddle")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	args := []string{"build", "-o", bin, module + "/src/cli"}
	if os.Getenv("GOCOVERDIR") != "" {
		args = []string{"build", "-cover", "-coverpkg=" + module + "/src/...", "-o", bin, module + "/src/cli"}
	}
	if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
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
