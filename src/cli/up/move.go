package up

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
)

// NewMoveCmd builds `poddle move <name>`: re-home a pod's session onto a fresh
// shell — a different size, image, or (later) host — while keeping its workspace
// and agent state (the named volumes). This is the answer to "needs more RAM":
// you don't resize memory in place, you move to a right-sized shell.
//
// It removes the old shell (keeping its volumes), recreates one with the same
// name + volumes (no re-clone), and re-brokers fresh handles.
func NewMoveCmd(a *app.App, b podBroker) *cobra.Command {
	var size, image, templateName, harnessName string
	var detach bool
	c := &cobra.Command{
		Use:   "move <name>",
		Short: "Re-home a pod's session onto a fresh, re-sized shell (keeps workspace + state)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			mode, _ := a.Engine.PodMode(name) // how the agent ran, so we resume it the same way
			_ = b.RevokePod(name)             // best-effort: retire the old shell's handles

			spec, h, _, err := buildSpec(cmd, a, b, buildOpts{
				name: name, image: image, size: size, templateName: templateName,
				harnessName: harnessName,
				withVolumes: true, // reuse the existing session volumes
				skipClone:   true, // the workspace volume already has the code
			})
			if err != nil {
				return err
			}
			spec.Mode = mode                              // preserve the mode across the move
			if err := a.Engine.Remove(name); err != nil { // remove old shell; named volumes persist
				return err
			}
			id, err := a.Engine.Create(spec)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), id)

			// Auto-resume the agent's conversation in the same mode (the state
			// volume carried over the history). Resume is harness-specific.
			switch {
			case mode == "headless":
				if r := h.ResumeCommand("headless"); r != "" {
					if err := a.Engine.ExecDetached(id, "( "+r+" ) > "+TaskLogPath+" 2>&1"); err != nil {
						return err
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "resumed %q headless — `poddle logs %s` to watch\n", name, name)
					return nil
				}
			case mode == "interactive" && !detach:
				if r := h.ResumeCommand("interactive"); r != "" {
					return a.Engine.ExecTTY(id, r) // reattach, resuming the conversation
				}
			}
			if detach {
				return nil
			}
			return a.Engine.Attach(id)
		},
	}
	c.Flags().StringVar(&size, "size", "", "new resource size (weak|strong)")
	c.Flags().StringVar(&image, "image", "", "new base image")
	c.Flags().StringVar(&templateName, "template", "", "template to resolve identity/connectors from")
	c.Flags().StringVar(&harnessName, "harness", "claude-code", "coding-agent runtime (must match the original for state)")
	c.Flags().BoolVarP(&detach, "detach", "d", false, "recreate without attaching")
	return c
}
