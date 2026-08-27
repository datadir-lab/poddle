//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
