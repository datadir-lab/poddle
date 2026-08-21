//go:build e2e

package e2e

import (
	"encoding/base64"
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

const (
	nodeImg = "docker.io/library/node:22"
	pyImg   = "docker.io/library/python:3"
)

// mockService records the Authorization header of every request. The in-pod
// client will usually fail parsing the empty 200 — the request + auth are what
// we assert. Git's no-auth first hop is answered by the broker's 401+challenge,
// so this upstream only ever sees the post-swap (real-credential) request.
//
// The broker is a container, so the mock binds 0.0.0.0 and is dialed by the
// broker at host.containers.internal (see brokerURL).
func mockService(t *testing.T, auths *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*auths = append(*auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// brokerURL rebuilds a 0.0.0.0-bound httptest server's URL as the broker
// container would reach it: via host.containers.internal, not 127.0.0.1/0.0.0.0.
func brokerURL(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("mock addr: %v", err)
	}
	return "http://host.containers.internal:" + port
}

func basicWant(user string) func(string) string {
	return func(s string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+s))
	}
}
func bearerWant() func(string) string { return func(s string) string { return "Bearer " + s } }

// in-pod commands — each triggers a request that must route through the broker.
func gitClone(m string) string {
	return "git clone " + m + "/datadir/r.git /tmp/r 2>/dev/null || true; echo DONE"
}
func bearerEnv(server, token string) func(string) string {
	return func(string) string {
		return `curl -s -o /dev/null -H "Authorization: Bearer $` + token + `" "$` + server + `/api/user" || true; echo DONE`
	}
}

// argocd exports ARGOCD_SERVER as host:port (no scheme); simulate its client
// with curl. Proves the Bearer swap — the argocd CLI's gRPC-web leg is separate.
func argocdCurl(string) string {
	return `curl -s -o /dev/null -H "Authorization: Bearer $ARGOCD_AUTH_TOKEN" "http://$ARGOCD_SERVER/api/v1/session" || true; echo DONE`
}

// jenkins puts the handle in JENKINS_URL's userinfo — curl sends it as Basic
// preemptively (the jenkins-cli.jar -auth path is separate).
func jenkinsCurl(string) string {
	return `curl -s -o /dev/null "$JENKINS_URL/api/json" || true; echo DONE`
}

func npmView(string) string { return "npm view express version 2>/dev/null || true; echo DONE" }
func pipInstall(string) string {
	return "pip install --disable-pip-version-check nonexistent-poddle-pkg 2>/dev/null || true; echo DONE"
}

// docker: read back the auths entry the connector wrote and replay it at the
// broker (host = the entry's key, auth = its base64 handle:x). Proves the
// config.json wiring + the broker's Basic swap without a container daemon.
func dockerReplay(string) string {
	return `H=$(grep -o '"[^"]*":{"auth"' ~/.docker/config.json | head -1 | cut -d'"' -f2); ` +
		`A=$(grep -o '"auth":"[^"]*"' ~/.docker/config.json | cut -d'"' -f4); ` +
		`curl -s -o /dev/null -H "Authorization: Basic $A" "http://$H/v2/" || true; echo DONE`
}

type connCase struct {
	name      string
	connector string
	user      string // basic user (empty for bearer)
	image     string
	inPod     func(mockURL string) string
	wantAuth  func(sentinel string) string
}

// connCases covers every built-in connector: the two auth modes (Basic /
// Bearer) across every pod-wiring (git rewrite, server/token env, package-
// manager config, docker config.json). Git hosts share the Basic+rewrite leg
// and override their default base_url to the mock.
var connCases = []connCase{
	// git hosts — Basic + git url.insteadOf
	{"forgejo", "forgejo", "me", nodeImg, gitClone, basicWant("me")},
	{"gitea", "gitea", "me", nodeImg, gitClone, basicWant("me")},
	{"github", "github", "me", nodeImg, gitClone, basicWant("me")},
	{"gitlab", "gitlab", "me", nodeImg, gitClone, basicWant("me")},
	{"bitbucket", "bitbucket", "me", nodeImg, gitClone, basicWant("me")},
	// CI — Bearer + server/token env
	{"woodpecker", "woodpecker", "", nodeImg, bearerEnv("WOODPECKER_SERVER", "WOODPECKER_TOKEN"), bearerWant()},
	{"drone", "drone", "", nodeImg, bearerEnv("DRONE_SERVER", "DRONE_TOKEN"), bearerWant()},
	{"argocd", "argocd", "", nodeImg, argocdCurl, bearerWant()},
	{"jenkins", "jenkins", "me", nodeImg, jenkinsCurl, basicWant("me")},
	// registries
	{"npm", "npm", "", nodeImg, npmView, bearerWant()},
	{"pypi", "pypi", "__token__", pyImg, pipInstall, basicWant("__token__")},
	{"docker", "docker", "me", nodeImg, dockerReplay, basicWant("me")},
}

// TestE2E_Connectors drives real `poddle up` for each built-in connector against
// podman: the pod's request routes through the broker, which swaps the handle
// for the real token — the upstream sees the real auth, never the handle.
func TestE2E_Connectors(t *testing.T) {
	requirePodman(t)
	bin := buildBinary(t)
	for _, tc := range connCases {
		t.Run(tc.name, func(t *testing.T) { runConnCase(t, bin, tc) })
	}
}

func runConnCase(t *testing.T, bin string, tc connCase) {
	var mu sync.Mutex
	var auths []string
	mock := mockService(t, &auths, &mu)
	mockAddr := brokerURL(t, mock)
	const sentinel = "SENTINEL-CONN"

	// A connection of this connector, upstream = the mock, in a throwaway dir.
	xdg := t.TempDir()
	connDir := filepath.Join(xdg, "poddle", "connections", "svc")
	meta := "connector = \"" + tc.connector + "\"\nbase_url = \"" + mockAddr + "\"\nowner = \"local\"\n"
	if tc.user != "" {
		meta += "user = \"" + tc.user + "\"\n"
	}
	writeFile(t, filepath.Join(connDir, "meta.toml"), meta)
	writeFile(t, filepath.Join(connDir, tc.connector+"-token"), sentinel)

	proj := t.TempDir()
	writeFile(t, filepath.Join(proj, ".poddle.toml"),
		"image = \""+tc.image+"\"\nconnectors = [\"svc\"]\n")

	pod := "poddle-conn-" + tc.name
	_ = exec.Command("podman", "rm", "-f", pod).Run()
	t.Cleanup(func() { _ = exec.Command("podman", "rm", "-f", pod).Run() })

	cmd := exec.Command(bin, "up", pod, "--exec", tc.inPod(mockAddr))
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("poddle up (%s) failed: %v\n%s", tc.connector, err, out)
	}
	if !strings.Contains(string(out), "DONE") {
		t.Fatalf("in-pod step did not run:\n%s", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(auths) == 0 {
		t.Fatalf("%s upstream received no requests — did not route through the broker:\n%s", tc.connector, out)
	}
	want := tc.wantAuth(sentinel)
	saw := false
	for _, a := range auths {
		if strings.Contains(a, "poddle_") {
			t.Errorf("the handle leaked to the upstream: %q", a)
		}
		if a == want {
			saw = true
		}
	}
	if !saw {
		t.Errorf("%s upstream never saw %q; got %v", tc.connector, want, auths)
	}
}
