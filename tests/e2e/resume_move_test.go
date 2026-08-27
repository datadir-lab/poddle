//go:build e2e

package e2e

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// resumeMoveCase is one harness with a real, non-empty ResumeCommand: claude-code
// (claudecode.go's `claude -p ... --continue`), codex (codex.go's `codex exec
// resume --last`), and aider (aider.go's `aider --message <nudge>
// --restore-chat-history`). pi/gemini/opencode return "" from ResumeCommand (resume
// unwired) and are deliberately excluded — moving one of them would only prove a
// no-op shell recreate, not resume. Fields mirror taskCase/mcpCase's per-harness
// quadruple (task_test.go / mcp_test.go) and are reused verbatim where a row
// already exists there.
type resumeMoveCase struct {
	name          string                                                               // subtest name / --harness
	harness       string                                                               // --harness (== name here)
	image         string                                                               // --image (must carry the harness's package manager)
	writeIdentity func(t *testing.T, xdg, sentinel string)                             // writeAnthropicIdentity / writeOpenAIIdentity (edit_test.go)
	upstreamEnv   string                                                               // provider base-URL env var poddle task/move read
	modelMock     func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server // this row's model-shaped mock upstream
	wantFirst     string                                                               // substring `poddle logs` must contain once the FIRST headless run completes
}

var resumeMoveCases = []resumeMoveCase{
	{
		// claude-code's --output-format json wraps the reply as `"result":"works"`
		// (mockAnthropicUpOn always replies "works") — the literal, pre-parametrization
		// assertion this row keeps verbatim.
		name:          "claude-code",
		harness:       "claude-code",
		image:         "docker.io/library/node:22",
		writeIdentity: writeAnthropicIdentity,
		upstreamEnv:   "PODDLE_ANTHROPIC_BASE_URL",
		// mockAnthropicUpOn (audit_test.go), not the loopback-only mockAnthropicUp: the
		// broker is a container and dials this mock at host.containers.internal, which
		// requires a 0.0.0.0 bind (same reasoning as mcpCases' claude-code row).
		modelMock: func(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
			return mockAnthropicUpOn(t, "0.0.0.0:0", auths, mu)
		},
		wantFirst: `"result":"works"`,
	},
	{
		// codex exec prints the mock's assistant text unwrapped — codexMarker surfaces
		// verbatim in combined stdout (taskCases' codex row proves this exact
		// TaskCommand shape reaches `poddle logs`).
		name:          "codex",
		harness:       "codex",
		image:         "docker.io/library/node:22",
		writeIdentity: writeOpenAIIdentity,
		upstreamEnv:   "PODDLE_OPENAI_BASE_URL",
		modelMock:     mockOpenAIUp,
		wantFirst:     codexMarker,
	},
	{
		// aider runs --no-stream --no-pretty and prints the assistant reply as plain
		// text — aiderMarker surfaces unwrapped (taskCases' aider row; mockOpenAIChatUp
		// matches --no-stream's flat-JSON chat.completions shape, not the SSE mocks).
		name:          "aider",
		harness:       "aider",
		image:         "docker.io/library/python:3.12",
		writeIdentity: writeOpenAIIdentity,
		upstreamEnv:   "PODDLE_OPENAI_BASE_URL",
		modelMock:     mockOpenAIChatUp,
		wantFirst:     aiderMarker,
	},
}

// TestE2E_ResumeMove_Headless proves resume-on-move end to end, for every harness
// with a real ResumeCommand (claude-code, codex, aider — see resumeMoveCase's doc
// comment). Per row: a detached (headless) `poddle task` runs that harness's coding
// agent whose conversation persists on a named state volume; `poddle move`
// recreates the shell on the carried-over volumes and, seeing the pod's `headless`
// mode, auto-resumes the agent — which reaches the mock upstream again through the
// broker. We assert the move landed on a NEW container that kept the `headless`
// label and the harness label, and that the resumed agent re-hit the upstream
// (never leaking the handle).
func TestE2E_ResumeMove_Headless(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)
	for _, tc := range resumeMoveCases {
		t.Run(tc.name, func(t *testing.T) { runResumeMoveCase(t, bin, tc) })
	}
}

// runResumeMoveCase drives one resumeMoveCase end to end. Everything is per-row and
// isolated (a fresh model mock instance, a fresh XDG dir, a uniquely-named pod, a
// distinct sentinel) so one row's traffic can never be mistaken for another's.
func runResumeMoveCase(t *testing.T, bin string, tc resumeMoveCase) {
	var mu sync.Mutex
	var auths []string
	mock := tc.modelMock(t, &auths, &mu)
	mockURL := mockURLFor(t, mock)
	sentinel := "SENTINEL-RESUME-" + strings.ToUpper(tc.name)
	cfg := t.TempDir()
	tc.writeIdentity(t, cfg, sentinel)

	// A BARE dir — deliberately no .poddle.toml. `task` sets image + identity +
	// harness via flags (which get labelled on the pod); `move`, run here with no
	// template and no --harness, must reconstruct image/identity/harness from the
	// pod's labels.
	proj := t.TempDir()

	pod := "poddle-resume-" + tc.name
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_, _ = exec.Command("sh", "-c",
			"podman volume ls -q --filter label=poddle.pod="+pod+" | xargs -r podman volume rm").CombinedOutput()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
	})

	// Isolate only the CLI config; DO NOT repoint XDG_RUNTIME_DIR — rootless
	// podman needs the real one (its own socket + the broker container's pasta
	// networking), and the shared broker container is the intended model.
	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+cfg,
		tc.upstreamEnv+"="+mockURL)

	run := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir, c.Env = proj, env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	inspect := func(format string) string {
		out, _ := exec.Command("podman", "inspect", "-f", format, pod).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	countAuths := func() int { mu.Lock(); defer mu.Unlock(); return len(auths) }

	// 1) Detached headless task — the agent runs and its conversation persists on
	//    the pod's state volume. --harness is passed explicitly (its own flag
	//    default is claude-code); image + identity are set via flags too, all of
	//    which get recorded as poddle.image / poddle.identity / poddle.harness
	//    labels.
	if out, err := run("task", "ping", "--identity", "work",
		"--harness", tc.harness,
		"--image", tc.image,
		"--name", pod, "--max-turns", "1", "--detach"); err != nil {
		t.Fatalf("task --detach failed: %v\n%s", err, out)
	}
	var firstLogs string
	for i := 0; i < 60; i++ { // wait for the first run to finish
		firstLogs, _ = run("logs", pod)
		if strings.Contains(firstLogs, tc.wantFirst) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !strings.Contains(firstLogs, tc.wantFirst) {
		t.Fatalf("first task run never completed; logs:\n%s", firstLogs)
	}
	id1 := inspect("{{.Id}}")
	if got := inspect(`{{index .Config.Labels "poddle.mode"}}`); got != "headless" {
		t.Fatalf("task pod should be labeled headless; got %q", got)
	}
	baseline := countAuths()

	// 2) Move to a bigger shell from a bare dir (no template) and — deliberately —
	//    no --harness flag: move.go's orLabel falls back to the pod's poddle.harness
	//    label (since the flag was never Changed), so move reconstructs THIS row's
	//    harness from the label alone. Headless mode then auto-resumes the agent
	//    via that reconstructed harness's ResumeCommand.
	if out, err := run("move", pod, "--size", "strong"); err != nil {
		t.Fatalf("move failed: %v\n%s", err, out)
	}
	id2 := inspect("{{.Id}}")
	if id1 == "" || id1 == id2 {
		t.Fatalf("move should recreate the shell (id1=%q id2=%q)", id1, id2)
	}
	if got := inspect(`{{index .Config.Labels "poddle.mode"}}`); got != "headless" {
		t.Errorf("moved shell should stay headless; got %q", got)
	}
	if got := inspect(`{{index .Config.Labels "poddle.harness"}}`); got != tc.harness {
		t.Errorf("moved shell should keep the harness label (proves label reconstruction); got %q want %q", got, tc.harness)
	}

	// 3) DECISIVE: the resumed agent re-hit the mock upstream through the broker —
	//    the auth count must climb strictly above the pre-move baseline. This is
	//    the proof that THIS harness's ResumeCommand actually fired, not a vacuous
	//    shell recreate.
	resumed := false
	for i := 0; i < 60; i++ {
		if countAuths() > baseline {
			resumed = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !resumed {
		last, _ := run("logs", pod)
		t.Fatalf("%s: resume-on-move never re-hit the upstream (baseline=%d now=%d); logs:\n%s",
			tc.name, baseline, countAuths(), last)
	}

	// The handle never leaked to the upstream; the broker swapped in the secret.
	assertSecretless(t, auths, sentinel, &mu)
}
