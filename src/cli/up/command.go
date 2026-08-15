// Package up implements `poddle up`: create a sandbox and connect to it.
package up

import (
	"fmt"

	"github.com/spf13/cobra"

	idn "github.com/datadir-lab/poddle/src/internal/identity"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
)

// creator is the narrow provider capability this slice needs.
type creator interface {
	Create(sandbox.Spec) (string, error)
	Attach(id string) error
}

// NewCmd builds the up command around a creator plus the identity store and
// provider registry (used by --identity).
func NewCmd(e creator, store *idn.Store, reg idn.Registry) *cobra.Command {
	var image, size, identityName string
	var detach bool

	c := &cobra.Command{
		Use:   "up [name]",
		Short: "Create a sandbox and connect to it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
				if err := applyIdentity(store, reg, identityName, &spec); err != nil {
					return err
				}
			}

			id, err := e.Create(spec)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), id)

			if detach {
				return nil
			}
			return e.Attach(id)
		},
	}
	c.Flags().StringVar(&image, "image", "docker.io/library/debian:stable-slim", "base image")
	c.Flags().StringVar(&size, "size", "weak", "resource size (weak|strong)")
	c.Flags().StringVar(&identityName, "identity", "", "coding-agent login to use in the sandbox")
	c.Flags().BoolVarP(&detach, "detach", "d", false, "create without attaching")
	return c
}

// applyIdentity resolves an identity, re-authenticates it on the client if it's
// stale (never a dead cred into the pod), and folds its materialization into
// the spec.
func applyIdentity(store *idn.Store, reg idn.Registry, name string, spec *sandbox.Spec) error {
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
	mat, err := p.Materialize(id)
	if err != nil {
		return err
	}
	for _, m := range mat.Mounts {
		spec.Mounts = append(spec.Mounts, sandbox.Mount(m))
	}
	if len(mat.Env) > 0 {
		if spec.Env == nil {
			spec.Env = map[string]string{}
		}
		for k, v := range mat.Env {
			spec.Env[k] = v
		}
	}
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
