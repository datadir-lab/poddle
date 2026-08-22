package poddled

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/podman"
	"github.com/datadir-lab/poddle/src/internal/policy"
)

// brokerLauncher is the podman surface EnsureRunning needs to bring up the
// broker container (satisfied by *podman.Provider).
type brokerLauncher interface {
	EnsureEgressNetwork(name string) error
	EnsureBroker(cfg podman.BrokerConfig) error
}

// Client talks to a running poddled over its Unix control socket, and can
// auto-start the daemon (as a container) if it isn't up.
type Client struct {
	socket string
	http   *http.Client
	// launcher brings up the broker container. Nil means EnsureRunning
	// constructs the default (podman.New(exec.OS{}, "")); tests inject a fake.
	launcher brokerLauncher
}

// NewClient returns a client for the socket at path (SocketPath() if empty).
func NewClient(path string) *Client {
	if path == "" {
		path = SocketPath()
	}
	return &Client{
		socket: path,
		http: &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		}},
	}
}

func (c *Client) uri(p string) string { return "http://unix" + p }

// Health reports whether the daemon is up and serving.
func (c *Client) Health() error {
	resp, err := c.http.Get(c.uri("/health"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("poddled health: %s", resp.Status)
	}
	return nil
}

// EnsureRunning starts poddled — as a dual-homed broker container via podman,
// egress-lockdown's placement — if it isn't already healthy, then waits for it
// to come up. Fail-closed: if the network or broker can't be brought up, the
// error is returned and there is no fallback to spawning a host process.
func (c *Client) EnsureRunning() error {
	if c.Health() == nil {
		return nil
	}
	// The broker container bind-mounts RunDir (its control socket) and StateDir
	// (its audit db); podman refuses to bind-mount a source that does not exist,
	// and podman itself needs XDG_RUNTIME_DIR present before any network/run call.
	// Creating RunDir also materializes its parent (XDG_RUNTIME_DIR). The old
	// host-process daemon created these in Serve(); the container launch must too.
	runDir := filepath.Dir(c.socket)
	stateDir := filepath.Dir(AuditDBPath())
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return fmt.Errorf("broker run dir: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("broker state dir: %w", err)
	}
	l := c.launcher
	if l == nil {
		l = podman.New(exec.OS{}, "")
	}
	if err := l.EnsureEgressNetwork("poddle-egress"); err != nil {
		return err
	}
	cfg := podman.BrokerConfig{
		Name:      "poddle-broker",
		Image:     resolveBrokerImage(),
		EgressNet: "poddle-egress",
		RunDir:    runDir,
		StateDir:  stateDir,
	}
	if err := l.EnsureBroker(cfg); err != nil {
		return err
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Health() == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("poddled did not become healthy on %s", c.socket)
}

// resolveBrokerImage resolves the broker container image ref: PODDLE_BROKER_IMAGE
// if set, else the ghcr image at this CLI's own version so a released poddle
// launches its matching broker (an unstamped "dev" build falls back to :latest).
func resolveBrokerImage() string {
	if img := os.Getenv("PODDLE_BROKER_IMAGE"); img != "" {
		return img
	}
	tag := "latest"
	if Version != "" && Version != "dev" {
		tag = strings.TrimPrefix(Version, "v") // matches publish-broker.yml (v0.1.3 -> 0.1.3)
	}
	return "ghcr.io/datadir-lab/poddle-broker:" + tag
}

// Version is the CLI's build version, stamped from main at startup so
// resolveBrokerImage can pin the broker image to the running CLI's version.
// "dev" (the default, unstamped) resolves to the :latest tag.
var Version = "dev"

// gatewayInfo fetches the daemon's pod-facing addresses.
func (c *Client) gatewayInfo() (addr, redis, postgres string, err error) {
	resp, err := c.http.Get(c.uri("/gateway"))
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	var out struct {
		Addr     string `json:"addr"`
		Redis    string `json:"redis"`
		Postgres string `json:"postgres"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", "", err
	}
	return out.Addr, out.Redis, out.Postgres, nil
}

// Gateway returns the pod-facing HTTP gateway address.
func (c *Client) Gateway() (string, error) {
	addr, _, _, err := c.gatewayInfo()
	if err != nil {
		return "", err
	}
	if addr == "" {
		return "", fmt.Errorf("gateway not ready")
	}
	return addr, nil
}

// RedisAddr returns the pod-facing L4 Redis address.
func (c *Client) RedisAddr() (string, error) {
	_, redis, _, err := c.gatewayInfo()
	if err != nil {
		return "", err
	}
	if redis == "" {
		return "", fmt.Errorf("L4 redis listener not ready")
	}
	return redis, nil
}

// PostgresAddr returns the pod-facing L4 Postgres address.
func (c *Client) PostgresAddr() (string, error) {
	_, _, postgres, err := c.gatewayInfo()
	if err != nil {
		return "", err
	}
	if postgres == "" {
		return "", fmt.Errorf("L4 postgres listener not ready")
	}
	return postgres, nil
}

// IssueHandle stores cred in the daemon and returns a handle tracked under pod
// (so RevokePod(pod) later invalidates it).
func (c *Client) IssueHandle(pod, scope string, cred broker.Credential) (string, error) {
	body, _ := json.Marshal(issueReq{Scope: scope, Credential: cred})
	resp, err := c.http.Post(c.uri("/pods/"+url.PathEscape(pod)+"/handles"), "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("issue handle: %s", resp.Status)
	}
	var out struct {
		Handle string `json:"handle"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Handle, nil
}

// Status fetches the daemon's status (addresses + active pods). An error means
// the daemon is not reachable (not running).
func (c *Client) Status() (Status, error) {
	resp, err := c.http.Get(c.uri("/status"))
	if err != nil {
		return Status{}, err
	}
	defer resp.Body.Close()
	var s Status
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return Status{}, err
	}
	return s, nil
}

// Egress mints a per-pod egress token and returns it plus the forward-proxy
// address, so the pod's arbitrary (non-brokered) egress can be routed through
// the broker (HTTP_PROXY) and governed by the pod's policy.
func (c *Client) Egress(pod string) (token, addr string, err error) {
	resp, err := c.http.Post(c.uri("/pods/"+url.PathEscape(pod)+"/egress"), "application/json", nil)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var v struct {
		Token string `json:"token"`
		Addr  string `json:"addr"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", "", err
	}
	return v.Token, v.Addr, nil
}

// SetPolicy binds a governance policy to a pod at the daemon; the gateway then
// enforces it on every request the pod makes.
func (c *Client) SetPolicy(pod string, p *policy.Policy) error {
	b, _ := json.Marshal(p)
	resp, err := c.http.Post(c.uri("/pods/"+url.PathEscape(pod)+"/policy"), "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("set policy: %s", resp.Status)
	}
	return nil
}

// Audit submits a client-side audit event (pod lifecycle, mount refusal) to the
// daemon. Best-effort by convention — callers ignore the error so a missing
// daemon never fails the underlying action.
func (c *Client) Audit(e audit.Event) error {
	b, _ := json.Marshal(e)
	resp, err := c.http.Post(c.uri("/audit"), "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("audit: %s", resp.Status)
	}
	return nil
}

// PushEvent records a host-autoscaler activity line as a daemon event, surfaced
// by `daemon status` and mirrored into the audit log. Mirrors Audit's shape;
// used by the host autoscaler so its grow/warn output reaches the broker.
func (c *Client) PushEvent(msg string) error {
	b, _ := json.Marshal(map[string]string{"msg": msg})
	resp, err := c.http.Post(c.uri("/events"), "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("push event: %s", resp.Status)
	}
	return nil
}

// Audits queries the daemon's audit log with the given filter (newest first).
func (c *Client) Audits(f audit.Filter) ([]audit.Event, error) {
	q := url.Values{}
	if f.Pod != "" {
		q.Set("pod", f.Pod)
	}
	if f.Kind != "" {
		q.Set("kind", f.Kind)
	}
	if f.Decision != "" {
		q.Set("decision", f.Decision)
	}
	if f.SinceSeq > 0 {
		q.Set("since", strconv.FormatInt(f.SinceSeq, 10))
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	resp, err := c.http.Get(c.uri("/audit") + "?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var events []audit.Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, err
	}
	return events, nil
}

// VerifyAudit checks the audit log's hash chain. ok is false and brokenAt names
// the seq of the first tampered/deleted row.
func (c *Client) VerifyAudit() (ok bool, brokenAt int64, err error) {
	resp, err := c.http.Get(c.uri("/audit/verify"))
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	var v struct {
		OK       bool  `json:"ok"`
		BrokenAt int64 `json:"brokenAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return false, 0, err
	}
	return v.OK, v.BrokenAt, nil
}

// RevokePod invalidates every handle the daemon issued for pod.
func (c *Client) RevokePod(pod string) error {
	req, _ := http.NewRequest(http.MethodDelete, c.uri("/pods/"+url.PathEscape(pod)), nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("revoke pod: %s", resp.Status)
	}
	return nil
}
