//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const cfgMarker = "PODDLE_USER_CFG_MARKER"

// TestE2E_HarnessConfig_NotClobbered proves a user's custom agent config, dropped
// at ~/.config/poddle/harness/<harness>/, is seeded into the pod's ConfigDir and is
// NOT overwritten by poddle's broker wiring — the codex/opencode clobber-fix. It
// seeds a config file carrying a distinctive marker, brings the pod up (which runs
// the harness Provisions), and asserts the marker survives in the pod's ConfigDir.
// No LLM mock is needed: the agent is never run, only installed.
func TestE2E_HarnessConfig_NotClobbered(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	cases := []struct {
		name          string
		harness       string
		image         string
		seedRel       string                                   // file under the harness config dir to seed
		podPath       string                                   // where it lands in the pod (ConfigDir + seedRel)
		seed          string                                   // seeded content (contains cfgMarker)
		writeIdentity func(t *testing.T, cfg, sentinel string) // identity matching the harness's vendor
	}{
		{
			name:          "codex",
			harness:       "codex",
			image:         "docker.io/library/node:22",
			seedRel:       "config.toml",
			podPath:       "/root/.codex/config.toml",
			seed:          "# " + cfgMarker + "\n[mcp_servers.demo]\ncommand = \"echo\"\n",
			writeIdentity: writeOpenAIIdentity,
		},
		{
			name:          "opencode",
			harness:       "opencode",
			image:         "docker.io/library/node:22",
			seedRel:       "opencode.json",
			podPath:       "/root/.config/opencode/opencode.json",
			seed:          `{"$schema":"https://opencode.ai/config.json","mcp":{"` + cfgMarker + `":{"type":"local","command":["echo"]}}}`,
			writeIdentity: writeOpenAIIdentity,
		},
		{
			name:          "pi",
			harness:       "pi",
			image:         "docker.io/library/node:22",
			seedRel:       "mcp.json",
			podPath:       "/root/.pi/mcp.json",
			seed:          `{"` + cfgMarker + `":{"command":"echo"}}`,
			writeIdentity: writeOpenAIIdentity,
		},
		{
			// gemini's vendor is google, so it needs a google identity (an openai
			// identity is rejected at `up` with "harness gemini does not support
			// vendor openai"). This row also exercises gemini's authSettingsMerge
			// Setup step (node-merges security.auth.selectedType into settings.json),
			// so it proves merge-not-clobber, like the claude onboarding-merge test.
			name:          "gemini",
			harness:       "gemini",
			image:         "docker.io/library/node:22",
			seedRel:       "settings.json",
			podPath:       "/root/.gemini/settings.json",
			seed:          `{"` + cfgMarker + `":true,"theme":"Default"}`,
			writeIdentity: writeGoogleIdentity,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			const sentinel = "SENTINEL-CFG"
			cfg := t.TempDir()
			tc.writeIdentity(t, cfg, sentinel)
			env := append(os.Environ(), "XDG_CONFIG_HOME="+cfg)

			seedDir := filepath.Join(cfg, "poddle", "harness", tc.harness)
			if err := os.MkdirAll(seedDir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(seedDir, tc.seedRel), tc.seed)

			pod := "poddle-cfg-" + tc.name
			_ = exec.Command("podman", "rm", "-f", pod).Run()
			t.Cleanup(func() {
				down := exec.Command(bin, "down", pod)
				down.Env = env
				_ = down.Run()
				_ = exec.Command("podman", "rm", "-f", pod).Run()
				_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
				_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
			})

			up := exec.Command(bin, "up", pod, "--detach",
				"--identity", "work", "--harness", tc.harness, "--image", tc.image)
			up.Env = env
			if out, err := up.CombinedOutput(); err != nil {
				t.Fatalf("up --detach failed: %v\n%s", err, out)
			}

			// The seeded config must be present in the pod's ConfigDir, unclobbered by
			// the harness Provisions (which no longer write the user's config file).
			out, err := exec.Command("podman", "exec", pod, "cat", tc.podPath).CombinedOutput()
			if err != nil {
				t.Fatalf("cat %s from pod: %v\n%s", tc.podPath, err, out)
			}
			if !strings.Contains(string(out), cfgMarker) {
				t.Fatalf("seeded %s was clobbered — marker %q missing; got:\n%s", tc.seedRel, cfgMarker, out)
			}
		})
	}
}

// TestE2E_HarnessConfig_ClaudeOnboardingMerge proves claude-code's onboarding write
// MERGES into ~/.claude.json (preserving a user's mcpServers/settings) rather than
// overwriting it. No LLM mock: it seeds a marker config, runs the exact merge the
// harness runs, and asserts the marker survives.
func TestE2E_HarnessConfig_ClaudeOnboardingMerge(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const marker = "PODDLE_CLAUDE_MARKER"
	const sentinel = "SENTINEL-CLAUDE-CFG"
	cfg := t.TempDir()
	writeAnthropicIdentity(t, cfg, sentinel)
	env := append(os.Environ(), "XDG_CONFIG_HOME="+cfg)

	pod := "poddle-cfg-claude"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run()
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	up := exec.Command(bin, "up", pod, "--detach",
		"--identity", "work", "--harness", "claude-code", "--image", "docker.io/library/node:22")
	up.Env = env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// A user's ~/.claude.json with an MCP marker, as if they configured it.
	seed := exec.Command(bin, "run", pod,
		`printf '%s' '{"mcpServers":{"demo":{"command":"echo"}},"marker":"`+marker+`"}' > $HOME/.claude.json`)
	seed.Env = env
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed ~/.claude.json failed: %v\n%s", err, out)
	}

	// Run the exact onboarding merge the harness runs on a task/resume.
	merge := exec.Command(bin, "run", pod,
		`node -e 'const f=process.env.HOME+"/.claude.json",fs=require("fs");let c={};try{c=JSON.parse(fs.readFileSync(f,"utf8"))}catch(e){};c.hasCompletedOnboarding=true;fs.writeFileSync(f,JSON.stringify(c))'`)
	merge.Env = env
	if out, err := merge.CombinedOutput(); err != nil {
		t.Fatalf("onboarding merge failed: %v\n%s", err, out)
	}

	out, err := exec.Command("podman", "exec", pod, "cat", "/root/.claude.json").CombinedOutput()
	if err != nil {
		t.Fatalf("cat ~/.claude.json: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, marker) {
		t.Fatalf("onboarding write clobbered the user's ~/.claude.json (marker %q gone):\n%s", marker, got)
	}
	if !strings.Contains(got, "hasCompletedOnboarding") {
		t.Fatalf("onboarding flag not set:\n%s", got)
	}
}

// aiderProjectMarker is the assistant reply text mockOpenAIChatUpRecordModel
// returns — distinctive so a naive success check can't be satisfied by aider
// echoing the prompt.
const aiderProjectMarker = "PODDLEAIDERCFGOK"

// mockOpenAIChatUpRecordModel is a model-recording variant of mockOpenAIChatUp
// (tests/e2e/secretless_up_test.go): a minimal plain-JSON OpenAI chat.completions
// mock (aider runs with --no-stream) that ALSO parses each request body's "model"
// field into models, guarded by mu — mirrors the auths recording pattern. This is
// the decisive observable for TestE2E_HarnessConfig_AiderProjectFile: the test
// invokes aider WITHOUT --model, so whatever model rides on the wire came from
// aider's own config resolution, not a flag. Binds 0.0.0.0 so the broker container
// reaches it at host.containers.internal.
func mockOpenAIChatUpRecordModel(t *testing.T, auths, models *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &payload)
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		*models = append(*models, payload.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-mock","object":"chat.completion","created":1,"model":"` + payload.Model +
			`","choices":[{"index":0,"message":{"role":"assistant","content":"` + aiderProjectMarker +
			`"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind 0.0.0.0: %v", err)
	}
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// TestE2E_HarnessConfig_AiderProjectFile proves aider HONORS a user's
// project-level config (.aider.conf.yml in the git workdir) even though poddle
// seeds no aider ConfigDir. aider's user config is a PROJECT file, read from
// $HOME/git-root/cwd, not a per-user dotfile; poddle's aider pod already uses
// /workspace (a persisted volume) as the git workdir, and poddle writes no aider
// config file at all — see aider.go's ConfigDir doc comment, which is the
// resolved (not deferred) design this test backs up.
//
// The decisive proof: the project config sets the model, aider is invoked
// WITHOUT --model, and the mock records the model that actually rode on the
// wire. If /workspace/.aider.conf.yml were ignored, aider would fall back to
// its own built-in default model — which is not gpt-4o-mini — so a match is
// only possible if aider read the project file.
func TestE2E_HarnessConfig_AiderProjectFile(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const sentinel = "SENTINEL-AIDER-PROJECT-CFG"
	const projectModel = "gpt-4o-mini" // distinctive from aider's built-in default and from other e2e tests' gpt-4o
	var mu sync.Mutex
	var auths, models []string
	mock := mockOpenAIChatUpRecordModel(t, &auths, &models, &mu)
	mockURL := mockURLFor(t, mock)

	cfg := t.TempDir()
	writeOpenAIIdentity(t, cfg, sentinel)
	env := append(os.Environ(), "XDG_CONFIG_HOME="+cfg, "PODDLE_OPENAI_BASE_URL="+mockURL)

	pod := "poddle-cfg-aider-project"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run()
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	// Bring the pod up (installs aider via the harness Provisions) and keep it.
	up := exec.Command(bin, "up", pod, "--detach",
		"--identity", "work", "--harness", "aider",
		"--image", "docker.io/library/python:3.12")
	up.Env = env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --detach failed: %v\n%s", err, out)
	}

	// Seed the USER's project-level aider config directly in the git workdir
	// (/workspace) — not a poddle-seeded ConfigDir; poddle never writes this file.
	seed := exec.Command(bin, "run", pod,
		"printf '%s' 'model: "+projectModel+"' > /workspace/.aider.conf.yml")
	seed.Env = env
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed /workspace/.aider.conf.yml failed: %v\n%s", err, out)
	}

	// Run aider headless, deliberately WITHOUT --model: a flag would override the
	// project config and mask the very thing under test (aider flags win over
	// config, so omitting --model is what makes the config the sole source).
	runCmd := exec.Command(bin, "run", pod,
		"cd /workspace && aider --message 'reply' --yes-always --no-stream --no-pretty --no-check-update --no-analytics")
	runCmd.Env = env
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("aider run failed: %v\n%s", err, out)
	}

	mu.Lock()
	gotModels := append([]string(nil), models...)
	mu.Unlock()
	found := false
	for _, m := range gotModels {
		if strings.Contains(m, projectModel) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("aider never sent model %q on the wire — project config /workspace/.aider.conf.yml was not honored; got models: %v", projectModel, gotModels)
	}

	assertSecretless(t, auths, sentinel, &mu)
}
