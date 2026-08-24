//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2E_Intercept_CASharedThroughBroker proves opt-in TLS interception works
// through the CONTAINERIZED broker — the shared-CA fix. The broker generates the
// egress CA on its bind-mounted state dir, `up` injects THAT cert into the pod's
// trust store, and the pod accepts the broker's leaf during an intercepted HTTPS
// handshake. Before the fix the pod trusted a host-side CA while the broker signed
// from a different container-local CA, so the handshake failed.
//
// It stays offline and deterministic: the policy intercepts a host and allows only
// GET, so a POST is BLOCKED at the broker (403) BEFORE any re-origination to the
// real upstream (a reserved .example host that is never dialed). A 403 therefore
// proves BOTH that the leaf was trusted (TLS was terminated) and that the method
// rule was enforced on HTTPS. A broken CA would instead yield a curl TLS error.
func TestE2E_Intercept_CASharedThroughBroker(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	const host = "intercept.example" // reserved TLD; never resolved/dialed (POST blocked first)
	xdg := t.TempDir()
	writeFile(t, filepath.Join(xdg, "poddle", "policies", "mitm.toml"),
		"allow_upstreams = [\""+host+"\"]\nintercept_hosts = [\""+host+"\"]\n\n[methods]\n\""+host+"\" = [\"GET\"]\n")

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"), "image = \"docker.io/library/node:22\"\n")

	// Isolate only the CLI config; DO NOT repoint XDG_RUNTIME_DIR — rootless podman
	// needs the real one, and the shared broker container is the intended model.
	env := append(os.Environ(), "XDG_CONFIG_HOME="+xdg)

	pod := "poddle-intercept-e2e"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run()
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "rm", "-f", "poddle-broker").Run() // fresh broker per test (state dir is this test's temp)
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	up := exec.Command(bin, "up", pod, "--detach", "--policy", "mitm")
	up.Dir, up.Env = proj, env
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("up --policy mitm failed: %v\n%s", err, out)
	}

	// POST is method-blocked on the intercepted host. `poddle run` wraps the command
	// in `sh -c`, so pass it as one arg. A 403 means curl completed the intercepted
	// TLS handshake — it trusted the broker's leaf, so the shared CA works — and the
	// broker enforced the method rule on the now-visible HTTPS request.
	r := exec.Command(bin, "run", pod, `curl -s -o /dev/null -w "%{http_code}" -X POST https://`+host+`/`)
	r.Env = env
	out, err := r.CombinedOutput()
	if err != nil {
		t.Fatalf("run curl failed (a TLS error here means the pod did not trust the broker's CA): %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); !strings.Contains(got, "403") {
		t.Fatalf("expected 403 (leaf trusted + method blocked on intercepted HTTPS), got %q — 000/TLS error means the shared CA is broken", got)
	}
}
