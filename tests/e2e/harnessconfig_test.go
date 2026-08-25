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
		name    string
		harness string
		image   string
		seedRel string // file under the harness config dir to seed
		podPath string // where it lands in the pod (ConfigDir + seedRel)
		seed    string // seeded content (contains cfgMarker)
	}{
		{
			name:    "codex",
			harness: "codex",
			image:   "docker.io/library/node:22",
			seedRel: "config.toml",
			podPath: "/root/.codex/config.toml",
			seed:    "# " + cfgMarker + "\n[mcp_servers.demo]\ncommand = \"echo\"\n",
		},
		{
			name:    "opencode",
			harness: "opencode",
			image:   "docker.io/library/node:22",
			seedRel: "opencode.json",
			podPath: "/root/.config/opencode/opencode.json",
			seed:    `{"$schema":"https://opencode.ai/config.json","mcp":{"` + cfgMarker + `":{"type":"local","command":["echo"]}}}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			const sentinel = "SENTINEL-CFG"
			cfg := t.TempDir()
			writeOpenAIIdentity(t, cfg, sentinel)
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
