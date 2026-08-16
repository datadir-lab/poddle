// Package up implements `poddle up`: create a sandbox and connect to it.
package up

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/harness"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
)

// NewCmd builds the up command. --identity resolves an auth provider; --harness
// resolves a pod-side runtime. b is the up-scoped secretless broker.
func NewCmd(a *app.App, b *broker.Broker) *cobra.Command {
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
				if err := applyIdentity(b, a.Identities, a.Providers, h, identityName, &spec); err != nil {
					return err
				}
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
// env + install commands into the spec. The real secret never touches the pod.
func applyIdentity(b *broker.Broker, store *idn.Store, reg idn.Registry, h harness.Harness, name string, spec *sandbox.Spec) error {
	id, err := store.Get(name)
	if err != nil {
		return fmt.Errorf("identity %q: %w", name, err)
	}
	p, ok := reg.Get(id.Provider)
	if !ok {
		return fmt.Errorf("unknown provider %q for identity %q", id.Provider, name)
	}
	authed, err := p.IsAuthenticated(id)
	if err != nil {
		return err
	}
	if !authed {
		if err := p.Authenticate(id); err != nil {
			return fmt.Errorf("authenticate %q: %w", name, err)
		}
	}
	cred, err := p.Credential(id)
	if err != nil {
		return err
	}
	if !h.Supports(cred.Vendor) {
		return fmt.Errorf("harness %q does not support vendor %q", h.Name(), cred.Vendor)
	}

	credID, err := b.Store(cred)
	if err != nil {
		return err
	}
	handle, err := b.IssueHandle(credID, spec.Name, 0) // 0 → DefaultHandleTTL, scope = pod name
	if err != nil {
		return err
	}

	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	for k, v := range h.Env(b.Addr(), handle.Value) {
		spec.Env[k] = v
	}
	spec.Setup = append(spec.Setup, h.Provisions()...)
	return nil
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
