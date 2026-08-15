// Package podman implements the sandbox provider backed by Podman, local or
// remote. A remote host is addressed via Podman's --url ssh://... transport, so
// the same code lists/creates/execs sandboxes regardless of where they run.
package podman

import (
	"encoding/json"
	"fmt"
	"strings"

	"git.dev.datadir.co/datadir/poddle/src/internal/exec"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

// Provider talks to a Podman engine. Conn is empty for local, or an ssh URL
// (e.g. "ssh://user@host/run/user/1000/podman/podman.sock") for a remote host.
type Provider struct {
	Runner exec.Runner
	Conn   string
}

// New returns a Provider using the given runner and connection (empty = local).
func New(r exec.Runner, conn string) *Provider {
	return &Provider{Runner: r, Conn: conn}
}

// podman prepends the connection URL (if any) before the given args.
func (p *Provider) podman(args ...string) []string {
	if p.Conn != "" {
		return append([]string{"--url", p.Conn}, args...)
	}
	return args
}

// List returns every poddle-managed sandbox on the target engine, any state.
func (p *Provider) List() ([]sandbox.Sandbox, error) {
	args := p.podman("ps", "-a",
		"--filter", "label=poddle.managed=true",
		"--format", "json")
	res, err := p.Runner.Run("podman", args...)
	if err != nil {
		return nil, fmt.Errorf("podman ps: %w: %s", err, res.Stderr)
	}
	return parseList(res.Stdout)
}

// Create starts a detached container for the spec and returns its id. The
// container runs `sleep infinity` so it stays alive to be attached to.
func (p *Provider) Create(s sandbox.Spec) (string, error) {
	args := p.podman("run", "-d",
		"--name", s.Name,
		"--label", "poddle.managed=true",
		"--label", "poddle.name="+s.Name,
		"--label", "poddle.template="+s.Template,
		"--label", "poddle.runtime="+s.Runtime,
		"--label", "poddle.size="+s.Size,
		"--label", "poddle.repo="+s.Repo,
	)
	if s.CPUs > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%g", s.CPUs))
	}
	if s.Memory != "" {
		args = append(args, "--memory", s.Memory)
	}
	args = append(args, s.Image, "sleep", "infinity")

	res, err := p.Runner.Run("podman", args...)
	if err != nil {
		return "", fmt.Errorf("podman run: %w: %s", err, res.Stderr)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// Attach opens an interactive shell inside the sandbox (bash if present, else sh).
func (p *Provider) Attach(id string) error {
	args := p.podman("exec", "-it", id, "sh", "-c", "exec bash 2>/dev/null || exec sh")
	return p.Runner.RunInteractive("podman", args...)
}

// containerJSON mirrors the fields poddle needs from `podman ps --format json`.
type containerJSON struct {
	ID     string            `json:"Id"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

func parseList(stdout string) ([]sandbox.Sandbox, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}
	var raw []containerJSON
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, fmt.Errorf("parse podman output: %w", err)
	}
	out := make([]sandbox.Sandbox, 0, len(raw))
	for _, c := range raw {
		out = append(out, sandbox.Sandbox{
			ID:       shortID(c.ID),
			Name:     c.Labels["poddle.name"],
			Template: c.Labels["poddle.template"],
			Runtime:  c.Labels["poddle.runtime"],
			Size:     c.Labels["poddle.size"],
			Repo:     c.Labels["poddle.repo"],
			State:    mapState(c.State),
		})
	}
	return out, nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// mapState normalizes Podman container states to poddle's vocabulary.
func mapState(s string) string {
	switch strings.ToLower(s) {
	case "running":
		return "running"
	case "paused":
		return "paused"
	case "created", "exited", "stopped", "dead":
		return "stopped"
	default:
		return strings.ToLower(s)
	}
}
