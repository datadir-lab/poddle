//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestE2E_Lockdown is the adversarial proof of egress lockdown: a brokered pod
// cannot escape the broker or touch the control plane. A pod is brought up under
// a policy that allows only a single mock upstream; from *inside* the pod a
// script runs six probes and prints a unique marker for each:
//
//  1. raw egress to a public IP (proxied)      -> denied by policy (BLOCKED)
//  2. direct egress with the proxy env stripped -> no route out on --internal (BLOCKED)
//  3. DNS resolution                            -> no external resolver     (BLOCKED)
//  4. a control-plane verb on the data plane    -> unreachable / immutable  (BLOCKED)
//  5. the allow-listed host THROUGH the broker  -> 200; a denied host       -> broker 403
//  6. the blocked attempts are audited (daemon audit --pod <pod> --decision deny)
//
// If any escape probe (1-4) is NOT blocked, the test fails loudly — a pod that
// reaches the internet or mutates its own governance is a lockdown breach.
func TestE2E_Lockdown(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)

	// allow-listed host (127.0.0.1) and a denied host (127.0.0.2) — distinct
	// loopback aliases so the policy matches by host, exactly as forward_test.
	var allowHit, denyHit int32
	allowMock := mockOn(t, "127.0.0.1:0", &allowHit)
	denyMock := mockOn(t, "127.0.0.2:0", &denyHit)

	xdg := t.TempDir()
	// Lockdown policy: allow ONLY the mock upstream host; everything else denied.
	writeFile(t, filepath.Join(xdg, "poddle", "policies", "lockdown.toml"),
		"allow_upstreams = [\"127.0.0.1\"]\n")

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \"docker.io/library/node:22\"\n")

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"XDG_RUNTIME_DIR="+filepath.Join(xdg, "run"),
		"XDG_STATE_HOME="+filepath.Join(xdg, "state"))

	pod := "poddle-e2e-lockdown"
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() {
		down := exec.Command(bin, "down", pod)
		down.Env = env
		_ = down.Run() // disconnects the broker from the lock net, then removes the pod
		_ = exec.Command("podman", "rm", "-f", pod).Run()
		_ = exec.Command("podman", "network", "rm", "poddle-lock-"+pod).Run()
	})

	// The probe script runs entirely inside the pod (sh -c). Every probe is
	// written as `try && echo ESCAPED || echo BLOCKED` so the marker is
	// unambiguous and the script always exits 0 (so `up --exec` succeeds and we
	// can inspect the full output). curl and getent are present in node:22.
	script := fmt.Sprintf(`
# 1. raw egress to a public IP: routed through the proxy, must be denied by policy.
curl -fsS -m 4 -o /dev/null http://1.1.1.1/ 2>/dev/null && echo P1_NET_ESCAPED || echo P1_NET_BLOCKED
# 2. direct egress with the proxy env stripped: the --internal net has no route out.
env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy curl -fsS -m 4 -o /dev/null http://1.0.0.1/ 2>/dev/null && echo P2_PROXY_ESCAPED || echo P2_PROXY_BLOCKED
# 3. DNS: no external resolver reachable on the lock net.
getent hosts example.com >/dev/null 2>&1 && echo P3_DNS_ESCAPED || echo P3_DNS_BLOCKED
# 4. control plane: derive the broker's data-plane addr from the proxy env and try
#    to POST a policy change. There is no such verb on the data plane and the
#    control socket is host-only, so this can never mutate governance.
BROKER=$(printf '%%s' "$HTTP_PROXY" | sed -E 's#^[a-zA-Z]+://##; s#^[^@]*@##; s#/.*$##')
curl -fsS -m 4 -o /dev/null -X POST --data '{"allow_upstreams":["0.0.0.0/0"]}' "http://$BROKER/pods/%s/policy" 2>/dev/null && echo P4_CTRL_ESCAPED || echo P4_CTRL_BLOCKED
# 5. through the broker: the allow-listed host succeeds (200); a denied host is
#    stopped by the broker with 403 (proving the traffic transited the broker).
echo "P5_ALLOW:$(curl -s -m 6 -o /dev/null -w '%%{http_code}' %s 2>/dev/null)"
echo "P5_DENY:$(curl -s -m 6 -o /dev/null -w '%%{http_code}' %s 2>/dev/null)"
echo PROBES_DONE
`, pod, allowMock.URL, denyMock.URL)

	up := exec.Command(bin, "up", pod, "--policy", "lockdown", "--exec", script)
	up.Dir, up.Env = proj, env
	raw, err := up.CombinedOutput()
	out := string(raw)
	if err != nil {
		t.Fatalf("up --policy lockdown --exec failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "PROBES_DONE") {
		t.Fatalf("probe script did not run to completion:\n%s", out)
	}

	// Escape probes 1-4 MUST show their BLOCKED marker and MUST NOT show ESCAPED.
	for _, p := range []struct{ blocked, escaped, what string }{
		{"P1_NET_BLOCKED", "P1_NET_ESCAPED", "raw egress to a public IP"},
		{"P2_PROXY_BLOCKED", "P2_PROXY_ESCAPED", "direct egress with the proxy unset"},
		{"P3_DNS_BLOCKED", "P3_DNS_ESCAPED", "external DNS resolution"},
		{"P4_CTRL_BLOCKED", "P4_CTRL_ESCAPED", "a control-plane policy mutation"},
	} {
		if strings.Contains(out, p.escaped) || !strings.Contains(out, p.blocked) {
			t.Fatalf("LOCKDOWN BREACH: %s was NOT blocked (missing %q / saw %q)\n%s",
				p.what, p.blocked, p.escaped, out)
		}
	}

	// Through the broker: allow-listed host reaches the upstream, denied host is
	// refused by the broker with 403 (so the traffic went THROUGH the broker).
	if !strings.Contains(out, "P5_ALLOW:200") {
		t.Errorf("the allow-listed host should reach the upstream through the broker (want P5_ALLOW:200):\n%s", out)
	}
	if !strings.Contains(out, "P5_DENY:403") {
		t.Errorf("a denied host should be refused by the broker with 403 (want P5_DENY:403):\n%s", out)
	}
	if atomic.LoadInt32(&denyHit) != 0 {
		t.Errorf("a policy-denied request must never reach the upstream (hits=%d)", denyHit)
	}
	if atomic.LoadInt32(&allowHit) == 0 {
		t.Errorf("the allow-listed request should have reached the upstream")
	}

	// The blocked attempts are audited: the daemon records the forward-proxy
	// denials against the pod.
	audit := exec.Command(bin, "daemon", "audit", "--pod", pod, "--decision", "deny")
	audit.Env = env
	aout, _ := audit.CombinedOutput()
	if !strings.Contains(string(aout), pod) || !strings.Contains(string(aout), "deny") {
		t.Errorf("the blocked egress attempts should be audited for %q:\n%s", pod, aout)
	}
}
