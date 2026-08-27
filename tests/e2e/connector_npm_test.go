//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// npmRegistryPort/npmRegUser/npmRegPass/npmPkgName are the REAL verdaccio
// backend this test seeds and drives through the broker. The port is fixed
// (not :0, and deliberately NOT verdaccio's own default 4873 — a dev host
// running its own local verdaccio for everyday npm work would otherwise
// collide) so the pod-side npm invocation and the connection's --url can
// reference it without any coordination beyond the const. npmPkgName is
// unscoped: a scoped name (e.g. "@poddle/depthtest") would need either
// `--access public` or a publishConfig.access, since npm's CLI defaults
// scoped packages to restricted regardless of registry — sidestepped
// entirely by publishing unscoped, which the task's own instructions
// call out as an acceptable choice.
const (
	npmRegistryPort = "14873"
	npmRegUser      = "poddlenpm"
	npmRegPass      = "poddlenpmPASS1"
	npmPkgName      = "poddle-depthtest"
)

// verdaccioAddUserResp is the body verdaccio's PUT /-/user/org.couchdb.user:*
// endpoint returns on success — the same shape `npm adduser`/`npm login`
// itself parses, with the freshly minted API token in .token.
type verdaccioAddUserResp struct {
	OK    string `json:"ok"`
	ID    string `json:"id"`
	Token string `json:"token"`
}

// waitHTTPReady polls url with GET until it returns any HTTP response (not
// necessarily 200) or the deadline passes. verdaccio is a Node app: waitTCP's
// bare TCP accept can succeed slightly before the HTTP server is actually
// answering requests (storage/plugin init still running), so the mint-token
// PUT below needs this extra warm-up beyond waitTCP.
func waitHTTPReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("nothing answering http at %s", url)
}

// verdaccioAddUser mints a REAL registry token by hitting verdaccio's npm
// adduser HTTP API directly (the same PUT the `npm adduser`/`npm login` CLI
// flow makes) — verdaccio's default htpasswd plugin config has an unbounded
// max_users, so self-registration is allowed out of the box, no config
// mount needed. Implemented as a direct net/http PUT + json.Unmarshal rather
// than shelling out to curl: it is at least as robust (no dependency on a
// curl binary being on PATH, no shell-quoting of the JSON body) and this is
// already a Go test.
func verdaccioAddUser(t *testing.T, port, user, pass string) string {
	t.Helper()
	body, err := json.Marshal(map[string]string{"name": user, "password": pass})
	if err != nil {
		t.Fatalf("marshal verdaccio adduser payload: %v", err)
	}
	url := "http://127.0.0.1:" + port + "/-/user/org.couchdb.user:" + user
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build verdaccio adduser request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("verdaccio adduser request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("verdaccio adduser failed (%d): %s", resp.StatusCode, raw)
	}
	var parsed verdaccioAddUserResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse verdaccio adduser response %q: %v", raw, err)
	}
	if parsed.Token == "" {
		t.Fatalf("verdaccio adduser response had no token: %s", raw)
	}
	return parsed.Token
}

// TestE2E_Connectors_NpmPublish is the npm connector's DEPTH e2e: unlike
// TestE2E_Connectors's npm case (a read-only `npm view` against a mock that
// only records the Authorization header), this drives a REAL `npm publish` —
// PUT metadata + PUT tarball — through the broker's forward gateway to a REAL
// verdaccio registry, proving the broker relays npm's write protocol with the
// real token while the pod holds only a handle.
//
// Rides the e2e-connector Taskfile target (`-run TestE2E_Connectors`, a
// substring match) — no new Taskfile/workflow entry needed.
func TestE2E_Connectors_NpmPublish(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	// --- 1. A real verdaccio backend. ---
	//
	// Host networking (like connector_docker_test.go's registry:2 upstream):
	// the test process reaches it at 127.0.0.1, and the broker container
	// reaches it at host.containers.internal — both routes exist only via
	// --network=host. verdaccio's own Dockerfile CMD passes
	// `--listen $VERDACCIO_PROTOCOL://0.0.0.0:$VERDACCIO_PORT`, so the stock
	// image already binds 0.0.0.0 (not localhost-only) — no extra listen
	// override or mounted config.yaml needed.
	_ = exec.Command("podman", "rm", "-f", "poddle-verdaccio-upstream").Run()
	up := exec.Command("podman", "run", "-d", "--name", "poddle-verdaccio-upstream", "--network=host",
		"-e", "VERDACCIO_PORT="+npmRegistryPort,
		"docker.io/verdaccio/verdaccio")
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("start upstream verdaccio: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", "poddle-verdaccio-upstream").Run() })
	waitTCP(t, "127.0.0.1:"+npmRegistryPort)
	waitHTTPReady(t, "http://127.0.0.1:"+npmRegistryPort+"/")

	// --- 2. Mint a REAL token from verdaccio (host, non-interactive) — this
	// is the connection's real secret. ---
	token := verdaccioAddUser(t, npmRegistryPort, npmRegUser, npmRegPass)

	// --- 3. Connection + pod: `poddle connect add` (the real CLI path, not a
	// hand-written meta.toml) seals the real token; the npm connector's Setup
	// steps write the pod's ~/.npmrc pointed at the broker, with the handle
	// as _authToken. ---
	xdg := t.TempDir()
	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg)

	registryURL := "http://host.containers.internal:" + npmRegistryPort
	add := exec.Command(bin, "connect", "add", "npmdepth",
		"--connector", "npm", "--url", registryURL, "--token", token)
	add.Env = env
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("connect add npmdepth: %v\n%s", err, out)
	}

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \""+nodeImg+"\"\nconnectors = [\"npmdepth\"]\n")

	pod := "poddle-connector-depth-npm"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per test (its state dir is this test's temp)
	})

	// --- 4. Drive a real publish THROUGH the broker: the pod reads the
	// registry back out of its own ~/.npmrc (never told it directly) and
	// passes it explicitly to `npm publish --registry`, so the assertion
	// doesn't depend on npm's config-precedence rules picking the connector's
	// entry over some other default. ---
	script := `mkdir -p /tmp/pkg && cd /tmp/pkg && ` +
		`printf '{"name":"` + npmPkgName + `","version":"1.0.0"}' > package.json && ` +
		`echo "DEPTH_NPMRC:$(tr '\n' '|' < ~/.npmrc)" && ` +
		`REG=$(grep '^registry=' ~/.npmrc | head -1 | cut -d= -f2-) && ` +
		`npm publish --registry "$REG" && echo DEPTH_PUBLISH_OK`

	cmd := exec.Command(bin, "up", pod, "--exec", script)
	cmd.Dir = proj
	cmd.Env = env
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("poddle up --exec (npm depth) failed: %v\n%s", err, out)
	}

	// --- 5. Decisive assertions. ---

	// The real publish (PUT metadata + PUT tarball) actually ran.
	if !strings.Contains(out, "DEPTH_PUBLISH_OK") {
		t.Fatalf("npm publish through the broker did not succeed:\n%s", out)
	}
	// npm's own success line, not just our sentinel — proves npm itself
	// believes the publish landed, not merely that the shell chain exited 0.
	if !strings.Contains(out, npmPkgName+"@1.0.0") {
		t.Errorf("npm's own publish success output missing %s@1.0.0:\n%s", npmPkgName, out)
	}

	// Secretless: the pod's ~/.npmrc _authToken is a poddle_ handle, and the
	// real verdaccio token never entered the pod.
	npmrc := extractMarker(out, "DEPTH_NPMRC:")
	if npmrc == "" {
		t.Fatalf("could not read the pod's ~/.npmrc from output:\n%s", out)
	}
	if strings.Contains(npmrc, token) {
		t.Fatalf("the real verdaccio token leaked into the pod's ~/.npmrc: %q", npmrc)
	}
	if !strings.Contains(npmrc, "poddle_") {
		t.Errorf("pod's ~/.npmrc should carry a poddle_ handle as _authToken, got %q", npmrc)
	}
	if strings.Contains(out, token) {
		t.Errorf("the real verdaccio token appeared in --exec output:\n%s", out)
	}

	// Optional strengthening: the published package really landed in the
	// real registry — queried directly from the HOST, bypassing the broker
	// entirely, so this can only pass if npm's publish actually reached
	// verdaccio (not just the broker accepting the request).
	resp, err := http.Get("http://127.0.0.1:" + npmRegistryPort + "/" + npmPkgName)
	if err != nil {
		t.Fatalf("query verdaccio for the published package: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"1.0.0"`) {
		t.Errorf("expected verdaccio to show version 1.0.0 after the depth publish, got %d: %s", resp.StatusCode, body)
	}
}
