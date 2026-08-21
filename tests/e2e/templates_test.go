//go:build e2e

package e2e

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestE2E_Templates exercises templates end-to-end against real podman.
func TestE2E_Templates(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)
	t.Run("nested_default_full", func(t *testing.T) { templatesNestedFull(t, bin) })
	t.Run("named_template", func(t *testing.T) { templatesNamed(t, bin) })
}

// nested_default_full: a 3-level extends chain (base ← web ← project default),
// referenced scripts at each level, a cloned repo, an identity from the
// template, and the claude-code harness actually installed — all verified
// inside the pod, with the secretless swap still holding.
func templatesNestedFull(t *testing.T, bin string) {
	var mu sync.Mutex
	var auths []string
	mock := mockAnthropicUpOn(t, "0.0.0.0:0", &auths, &mu)
	// The broker container dials the upstream, so address the mock at
	// host.containers.internal (it binds 0.0.0.0), not the loopback mock.URL.
	_, mockPort, err := net.SplitHostPort(mock.Listener.Addr().String())
	if err != nil {
		t.Fatalf("mock addr: %v", err)
	}
	mockURL := "http://host.containers.internal:" + mockPort
	const sentinel = "SENTINEL-TPL"

	// User config: the `base` template (+ its script) and a sentinel identity.
	xdg := t.TempDir()
	writeFile(t, filepath.Join(xdg, "poddle", "templates", "base.toml"),
		"image = \"docker.io/library/node:22\"\nharness = \"claude-code\"\nidentity = \"work\"\nscripts = [\"scripts/base.sh\"]\n")
	writeFile(t, filepath.Join(xdg, "poddle", "templates", "scripts", "base.sh"), "touch /base-ran\n")
	writeFile(t, filepath.Join(xdg, "poddle", "identities", "work", "meta.toml"), "name = \"work\"\nprovider = \"anthropic\"\n")
	writeFile(t, filepath.Join(xdg, "poddle", "identities", "work", "anthropic-token"), sentinel)

	// Project: .poddle/web.toml (extends base) + root .poddle.toml (extends web).
	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle", "web.toml"), "extends = \"base\"\nscripts = [\"scripts/web.sh\"]\n")
	writeFile(t, filepath.Join(proj, ".poddle", "scripts", "web.sh"), "touch /web-ran\n")
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"extends = \"web\"\nsetup = [\"touch /app-ran\"]\nrepo = \"https://github.com/octocat/Hello-World.git\"\n")

	const pod = "poddle-tpl-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per test (its state dir is this test's temp)
	})

	// Verify (set -e aborts on any failed check → up returns non-zero):
	verify := strings.Join([]string{
		"set -e",
		"test -f /base-ran",            // base.sh ran (inherited via extends)
		"test -f /web-ran",             // web.sh ran (mid level)
		"test -f /app-ran",             // root inline setup ran
		"test -d /workspace/.git",      // repo cloned
		"command -v claude >/dev/null", // harness actually installed
		"export IS_SANDBOX=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"echo '{\"hasCompletedOnboarding\":true}' > $HOME/.claude.json",
		"claude -p \"ping\" --output-format json --max-turns 1 --dangerously-skip-permissions </dev/null",
	}, "; ")

	cmd := exec.Command(bin, "up", pod, "--exec", verify) // no --template → project default
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg, "PODDLE_ANTHROPIC_BASE_URL="+mockURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle up (templates) failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"result":"works"`) {
		t.Fatalf("claude did not run through the broker:\n%s", out)
	}

	mu.Lock()
	defer mu.Unlock()
	saw := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == "Bearer "+sentinel {
			saw = true
		}
	}
	if !saw {
		t.Errorf("mock never saw the sentinel secret: %v", auths)
	}
}

// named_template: `--template <name>` selects a specific template (different
// image + its own setup), independent of any project default.
func templatesNamed(t *testing.T, bin string) {
	xdg := t.TempDir()
	writeFile(t, filepath.Join(xdg, "poddle", "templates", "minimal.toml"),
		"image = \"docker.io/library/alpine:latest\"\nsetup = [\"touch /minimal-ran\"]\n")
	proj := t.TempDir() // no project config; selection is purely --template

	const pod = "poddle-tpl-named-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", pod).Run() })

	cmd := exec.Command(bin, "up", pod, "--template", "minimal", "--exec", "test -f /minimal-ran && echo TEMPLATE_OK")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("named-template up failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "TEMPLATE_OK") {
		t.Fatalf("named template setup did not run:\n%s", out)
	}
}
