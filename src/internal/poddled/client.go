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
	"os/exec"
	"time"

	"github.com/datadir-lab/poddle/src/internal/broker"
)

// Client talks to a running poddled over its Unix control socket, and can
// auto-start the daemon if it isn't up.
type Client struct {
	socket string
	http   *http.Client
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

// EnsureRunning starts poddled (this binary's `daemon` subcommand, detached) if
// it isn't already healthy, then waits for it to come up.
func (c *Client) EnsureRunning() error {
	if c.Health() == nil {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "daemon", "--socket", c.socket)
	cmd.SysProcAttr = detachAttrs()
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn poddled: %w", err)
	}
	_ = cmd.Process.Release() // it outlives this process

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Health() == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("poddled did not become healthy on %s", c.socket)
}

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
