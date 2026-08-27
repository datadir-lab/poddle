//go:build e2e

package e2e

import (
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// registryUser/registryPass are the REAL credentials for the registry:2
// upstream this test seeds and drives through the broker. They live only on
// the host (htpasswd file + the connection's sealed token file) and inside
// the broker's vault — never in the pod. registryPort is fixed (not :0) so
// the pod-side skopeo invocation and the connector's base_url can reference it
// without any coordination beyond the const.
const (
	registryUser = "poddlereg"
	registryPass = "poddleregpass"
	registryPort = "15050"
)

// depthSkopeoImage is a throwaway image, built locally on the HOST (normal
// network — this runs before any pod exists, not inside a locked pod), with
// skopeo pre-installed. Two problems make this necessary rather than using
// the official quay.io/skopeo/stable image directly as the pod's image:
//
//  1. That image sets ENTRYPOINT ["/usr/bin/skopeo"] (see
//     github.com/containers/image_build/blob/main/skopeo/Containerfile). Every
//     poddle pod is kept alive with `podman run -d <image> tail -f /dev/null`
//     (podman.go's Create) — with that entrypoint, the actual command becomes
//     `skopeo tail -f /dev/null`, which skopeo rejects as an unknown
//     subcommand, so the container would exit immediately and every
//     subsequent `podman exec` (Setup, then this test's --exec) would fail.
//  2. Installing skopeo as a pod-side Setup step (e.g. `apt-get install -y
//     skopeo`) is not a fix either: this pod has a connector, so `up`
//     binds it to a DERIVED DEFAULT-DENY policy scoped to exactly the
//     connector's host (the registry) — see cli/up/command.go's buildSpec,
//     the `case len(egressHosts) > 0` branch. A Setup-time apt-get needing
//     arbitrary internet (package mirrors) would be blocked by the very
//     lockdown this suite exists to prove (TestE2E_Lockdown).
//
// Building the image on the host, before any pod exists, sidesteps both: the
// resulting image has a normal (unset) entrypoint, so `tail -f /dev/null`
// works, and the pod itself never needs to reach anything but the registry.
func buildDepthSkopeoImage(t *testing.T) string {
	t.Helper()
	const tag = "poddle-skopeo-e2e:test"
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Containerfile"),
		"FROM docker.io/library/debian:stable-slim\n"+
			"RUN apt-get update && apt-get install -y --no-install-recommends skopeo ca-certificates && rm -rf /var/lib/apt/lists/*\n")
	cmd := exec.Command("podman", "build", "-t", tag, "-f", filepath.Join(dir, "Containerfile"), dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build skopeo pod image: %v\n%s", err, out)
	}
	return tag
}

// extractMarker returns the value after "marker:" up to the next newline in
// out, or "" if the marker never appears — used to pull the pod's own
// ~/.docker/config.json auth value out of its --exec output without a
// separate follow-up `poddle run`.
func extractMarker(out, marker string) string {
	i := strings.Index(out, marker)
	if i < 0 {
		return ""
	}
	rest := out[i+len(marker):]
	if j := strings.IndexAny(rest, "\r\n"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// TestE2E_Connectors_DockerRegistry is the docker connector's DEPTH e2e: unlike
// TestE2E_Connectors's docker case (which only replays the pod's config.json
// auth at a mock recorder with curl), this drives the REAL Docker Registry v2
// protocol — manifest + blob transfer, both a pull AND a push — through the
// broker's forward gateway to a REAL registry:2 backend, proving the broker
// relays a genuinely different, multi-request binary protocol end to end, not
// just a single Basic-auth header swap. Pods have no docker daemon, so the
// in-pod client is skopeo (userspace, no daemon/privilege needed).
//
// Rides the e2e-connector Taskfile target (`-run TestE2E_Connectors`, a
// substring match) — no new Taskfile/workflow entry needed.
func TestE2E_Connectors_DockerRegistry(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)
	podImage := buildDepthSkopeoImage(t)

	// --- 1. A real registry:2 backend, with real htpasswd Basic auth. ---
	//
	// The htpasswd file is generated with the registry image's OWN htpasswd
	// binary (the standard recipe: `--entrypoint htpasswd`), so its bcrypt
	// hash is guaranteed compatible with the registry's own auth middleware.
	htBytes, err := exec.Command("podman", "run", "--rm", "--entrypoint", "htpasswd",
		"docker.io/library/registry:2", "-Bbn", registryUser, registryPass).Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("generate htpasswd: %v\n%s", err, stderr)
	}
	htpasswdPath := filepath.Join(t.TempDir(), "htpasswd")
	writeFile(t, htpasswdPath, string(htBytes))

	_ = exec.Command("podman", "rm", "-f", "poddle-registry-upstream").Run()
	// Host networking (like l4_test.go's redis/postgres upstreams): the test
	// process reaches it at localhost, and the broker container reaches it at
	// host.containers.internal — both routes exist only via --network=host.
	up := exec.Command("podman", "run", "-d", "--name", "poddle-registry-upstream", "--network=host",
		"-v", htpasswdPath+":/auth/htpasswd:ro",
		"-e", "REGISTRY_HTTP_ADDR=0.0.0.0:"+registryPort,
		"-e", "REGISTRY_AUTH=htpasswd",
		"-e", "REGISTRY_AUTH_HTPASSWD_REALM=poddle",
		"-e", "REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd",
		"docker.io/library/registry:2")
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("start upstream registry: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", "poddle-registry-upstream").Run() })
	waitTCP(t, "127.0.0.1:"+registryPort)

	// --- 2. Seed a tiny real image into the registry, with the REAL creds,
	// directly on the host (NOT through the broker) — this is what the pod
	// will pull through the broker below. ---
	if out, err := exec.Command("podman", "pull", "docker.io/library/hello-world").CombinedOutput(); err != nil {
		t.Fatalf("pull seed image: %v\n%s", err, out)
	}
	seed := exec.Command("podman", "push", "--creds", registryUser+":"+registryPass, "--tls-verify=false",
		"docker.io/library/hello-world", "localhost:"+registryPort+"/depthtest:latest")
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seed real image into the registry: %v\n%s", err, out)
	}

	// --- 3. Connection + pod: `poddle connect add` (the real CLI path, not a
	// hand-written meta.toml) seals the real creds; the docker connector's
	// dockerLogin Setup step writes the pod's ~/.docker/config.json keyed by
	// the broker's own address, with the base64 of "<handle>:x" as auth. ---
	xdg := t.TempDir()
	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg)

	registryURL := "http://host.containers.internal:" + registryPort
	add := exec.Command(bin, "connect", "add", "dockerdepth",
		"--connector", "docker", "--url", registryURL,
		"--user", registryUser, "--token", registryPass)
	add.Env = env
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("connect add dockerdepth: %v\n%s", err, out)
	}

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \""+podImage+"\"\nconnectors = [\"dockerdepth\"]\n")

	pod := "poddle-connector-depth-docker"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per test (its state dir is this test's temp)
	})

	// --- 4. Drive a real pull AND a real push THROUGH the broker: the pod
	// learns the broker's address from its own config.json (the connector
	// wiring's auths key), never from anything this test tells it directly. ---
	script := `H=$(grep -o '"[^"]*":{"auth"' ~/.docker/config.json | head -1 | cut -d'"' -f2); ` +
		`A=$(grep -o '"auth":"[^"]*"' ~/.docker/config.json | cut -d'"' -f4); ` +
		`echo "DEPTH_AUTH:$A"; ` +
		`skopeo copy --src-tls-verify=false "docker://$H/depthtest:latest" oci:/tmp/pulled:latest && echo DEPTH_PULL_OK && ` +
		`skopeo copy --dest-tls-verify=false oci:/tmp/pulled:latest "docker://$H/depthtest:pushed" && echo DEPTH_PUSH_OK`

	cmd := exec.Command(bin, "up", pod, "--exec", script)
	cmd.Dir = proj
	cmd.Env = env
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("poddle up --exec (docker depth) failed: %v\n%s", err, out)
	}

	// --- 5. Decisive assertions. ---

	// The real Registry v2 manifest+blob protocol actually ran, both legs.
	if !strings.Contains(out, "DEPTH_PULL_OK") {
		t.Fatalf("skopeo pull through the broker did not succeed:\n%s", out)
	}
	if !strings.Contains(out, "DEPTH_PUSH_OK") {
		t.Fatalf("skopeo push through the broker did not succeed:\n%s", out)
	}

	// Secretless: the pod's config.json auth decodes to a poddle_ handle, and
	// the real registry password never entered the pod.
	authB64 := extractMarker(out, "DEPTH_AUTH:")
	if authB64 == "" {
		t.Fatalf("could not read the pod's docker config.json auth value from output:\n%s", out)
	}
	decoded, err := base64.StdEncoding.DecodeString(authB64)
	if err != nil {
		t.Fatalf("pod's docker config.json auth is not valid base64 (%q): %v", authB64, err)
	}
	cred := string(decoded)
	if strings.Contains(cred, registryPass) {
		t.Fatalf("the real registry password leaked into the pod's docker config.json: %q", cred)
	}
	if !strings.HasPrefix(cred, "poddle_") {
		t.Errorf("pod's docker config.json auth should decode to a poddle_ handle (handle:x), got %q", cred)
	}
	if strings.Contains(out, registryPass) {
		t.Errorf("the real registry password appeared in --exec output:\n%s", out)
	}

	// Optional strengthening: the pushed image really landed in the real
	// registry — queried directly from the HOST with the real creds, bypassing
	// the broker entirely, so this can only pass if skopeo's push actually
	// reached the upstream (not just the broker accepting the request).
	req, err := http.NewRequest(http.MethodGet, "http://localhost:"+registryPort+"/v2/depthtest/tags/list", nil)
	if err != nil {
		t.Fatalf("build tags/list request: %v", err)
	}
	req.SetBasicAuth(registryUser, registryPass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("query registry tags/list from the host: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "pushed") {
		t.Errorf("expected the registry to show the \"pushed\" tag after the depth push, got %d: %s", resp.StatusCode, body)
	}
}
