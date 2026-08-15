// Package up implements `poddle up`: create a sandbox and connect to it.
package up

import (
	"fmt"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
)

// creator is the narrow provider capability this slice needs.
type creator interface {
	Create(sandbox.Spec) (string, error)
	Attach(id string) error
}

// NewCmd builds the up command around a creator.
func NewCmd(e creator) *cobra.Command {
	var image, size string
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

			id, err := e.Create(sandbox.Spec{
				Name:     name,
				Image:    image,
				Template: "base",
				Runtime:  "container",
				Size:     size,
				CPUs:     cpus,
				Memory:   mem,
			})
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
	c.Flags().BoolVarP(&detach, "detach", "d", false, "create without attaching")
	return c
}

// resolveSize maps a size tier to concrete cpu/memory limits. (Later this moves
// to per-target config, since a tier's meaning depends on the host.)
func resolveSize(size string) (cpus float64, memory string) {
	switch size {
	case "strong":
		return 8, "16g"
	default: // weak
		return 2, "4g"
	}
}
