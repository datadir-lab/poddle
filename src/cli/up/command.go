// Package up implements `poddle up`: create a sandbox and connect to it.
package up

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/harness"
	idn "git.dev.datadir.co/datadir/poddle/src/internal/identity"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

// brokerBindAddr is where the up-scoped broker listens in Phase 1. It is
// loopback for now; making it reachable from the pod's container
// (host.containers.internal + a host-reachable bind) is task 1.14.
const brokerBindAddr = "127.0.0.1:0"

// credBroker is the broker capability `up` needs. The real *broker.Broker
// satisfies it; tests pass a spy. (Named to avoid clashing with the broker
// package import.)
type credBroker interface {
	Serve(addr string) (string, error)
	Addr() string
	Store(broker.Credential) (string, error)
	IssueHandle(credID, scope string, ttl time.Duration) (broker.Handle, error)
	Revoke(handleValue string)
	Stop(ctx context.Context) error
}

// NewCmd builds the up command. --identity resolves an auth provider; --harness
// resolves a pod-side runtime. b is the up-scoped secretless broker.
func NewCmd(a *app.App, b credBroker) *cobra.Command {
	var image, size, identityName, harnessName string
	var detach bool

	c := &cobra.Command{
		Use:   "up [name]",
		Short: "Create a sandbox and connect to it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, ok := a.Harnesses.Get(harnessName)
			if !ok {
				return fmt.Errorf("unknown harness %q", harnessName)
			}
			name := "poddle"
			if len(args) > 0 {
				name = args[0]
			}
			cpus, mem := resolveSize(size)
			spec := sandbox.Spec{
				Name: name, Image: image, Template: "base",
				Runtime: "container", Size: size, CPUs: cpus, Memory: mem,
			}
			if identityName != "" {
				if detach {
					return fmt.Errorf("--detach with --identity needs poddled (Phase 2); attach to keep the broker alive")
				}
				// Serve the broker so the harness env points at a live gateway,
				// then tear it down when the attached session ends.
				if _, err := b.Serve(brokerBindAddr); err != nil {
					return fmt.Errorf("serve broker: %w", err)
				}
				defer func() { _ = b.Stop(context.Background()) }()

				handle, err := applyIdentity(b, a.Identities, a.Providers, h, identityName, &spec)
				if err != nil {
					return err
				}
				defer b.Revoke(handle)
			}

			id, err := a.Engine.Create(spec)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), id)

			if detach {
				return nil
			}
			return a.Engine.Attach(id)
		},
	}
	c.Flags().StringVar(&image, "image", "docker.io/library/debian:stable-slim", "base image")
	c.Flags().StringVar(&size, "size", "weak", "resource size (weak|strong)")
	c.Flags().StringVar(&identityName, "identity", "", "coding-agent login to use in the sandbox")
	c.Flags().StringVar(&harnessName, "harness", "claude-code", "coding-agent runtime to run in the sandbox")
	c.Flags().BoolVarP(&detach, "detach", "d", false, "create without attaching")
	return c
}

// applyIdentity wires an identity into the pod secretlessly: re-authenticate on
// the client if stale (never a dead cred into the pod), take the real
// Credential, seal it in the broker, and fold ONLY the harness's broker-pointing
// env + install commands into the spec. Returns the issued handle so the caller
// can revoke it on teardown. The real secret never touches the pod.
func applyIdentity(b credBroker, store *idn.Store, reg idn.Registry, h harness.Harness, name string, spec *sandbox.Spec) (string, error) {
	id, err := store.Get(name)
	if err != nil {
		return "", fmt.Errorf("identity %q: %w", name, err)
	}
	p, ok := reg.Get(id.Provider)
	if !ok {
		return "", fmt.Errorf("unknown provider %q for identity %q", id.Provider, name)
	}
	authed, err := p.IsAuthenticated(id)
	if err != nil {
		return "", err
	}
	if !authed {
		if err := p.Authenticate(id); err != nil {
			return "", fmt.Errorf("authenticate %q: %w", name, err)
		}
	}
	cred, err := p.Credential(id)
	if err != nil {
		return "", err
	}
	if !h.Supports(cred.Vendor) {
		return "", fmt.Errorf("harness %q does not support vendor %q", h.Name(), cred.Vendor)
	}

	credID, err := b.Store(cred)
	if err != nil {
		return "", err
	}
	handle, err := b.IssueHandle(credID, spec.Name, 0) // 0 → DefaultHandleTTL, scope = pod name
	if err != nil {
		return "", err
	}

	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	for k, v := range h.Env("http://"+b.Addr(), handle.Value) {
		spec.Env[k] = v
	}
	spec.Setup = append(spec.Setup, h.Provisions()...)
	return handle.Value, nil
}

// resolveSize maps a size tier to concrete cpu/memory limits.
func resolveSize(size string) (cpus float64, memory string) {
	switch size {
	case "strong":
		return 8, "16g"
	default: // weak
		return 2, "4g"
	}
}
