package up

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
)

// NewResizeCmd builds `poddle resize <pod> [size]`: change a running pod's CPU
// ceiling / memory cap live, with no restart. Give a size tier (weak|strong) or
// explicit --cpus/--memory.
func NewResizeCmd(a *app.App) *cobra.Command {
	var cpus float64
	var memory string
	c := &cobra.Command{
		Use:   "resize <name|id> [size]",
		Short: "Change a running pod's CPU/memory live (no restart)",
		Example: `  # jump to a named size, or set CPU/memory directly
  poddle resize my-sandbox strong
  poddle resize my-sandbox --cpus 8 --memory 16g`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, m := 0.0, ""
			if len(args) == 2 {
				c, m = resolveSize(args[1])
			}
			if cmd.Flags().Changed("cpus") {
				c = cpus
			}
			if cmd.Flags().Changed("memory") {
				m = memory
			}
			if c == 0 && m == "" {
				return fmt.Errorf("specify a size (weak|strong) or --cpus/--memory")
			}
			return a.Engine.Resize(args[0], c, m)
		},
	}
	c.Flags().Float64Var(&cpus, "cpus", 0, "CPU ceiling in cores (bursts up to this; idle floats to ~0)")
	c.Flags().StringVar(&memory, "memory", "", "memory cap, e.g. 8g")
	return c
}
