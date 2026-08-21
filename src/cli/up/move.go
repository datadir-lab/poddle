package up

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/audit"
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
		Example: `  # move onto a bigger shell without losing workspace or agent state
  poddle move my-sandbox --size strong`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			info, _ := a.Engine.PodInfo(name) // reconstruct the pod from its own labels
			_ = b.RevokePod(name)             // best-effort: retire the old shell's handles

			bn, ok := a.Engine.(brokerNet)
			if !ok {
				return fmt.Errorf("egress lockdown needs the podman engine")
			}
			// Seed defaults from the existing pod so a context-free move (no cwd,
			// no template — e.g. the daemon's autoscaler) preserves everything but
			// the size. Precedence: explicit flag > --template > pod label > default.
			spec, h, _, err := buildSpec(cmd, a, b, bn, buildOpts{
				name: name, templateName: templateName,
				image:        orLabel(cmd, "image", image, info.Image),
				size:         orLabel(cmd, "size", size, info.Size),
				harnessName:  orLabel(cmd, "harness", harnessName, info.Harness),
				identityName: info.Identity, // move has no --identity flag; the label is the source
				autoscale:    info.Autoscale,
				withVolumes:  true, // reuse the existing session volumes
				skipClone:    true, // the workspace volume already has the code
			})
			if err != nil {
				return err
			}
			spec.Mode = info.Mode // preserve the mode across the move
			if spec.Repo == "" {
				spec.Repo = info.Repo // preserve the repo label when no template set it
			}
			if err := a.Engine.Remove(name); err != nil { // remove old shell; named volumes persist
				return err
			}
			id, err := a.Engine.Create(spec)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), id)
			_ = b.Audit(audit.Event{Pod: name, Kind: audit.KindPodMove, Detail: "size=" + spec.Size, Decision: audit.DecisionAllow})

			// Auto-resume the agent's conversation in the same mode (the state
			// volume carried over the history). Resume is harness-specific.
			switch {
			case info.Mode == "headless":
				if r := h.ResumeCommand("headless"); r != "" {
					if err := a.Engine.ExecDetached(id, "( "+r+" ) > "+TaskLogPath+" 2>&1"); err != nil {
						return err
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "resumed %q headless — `poddle logs %s` to watch\n", name, name)
					return nil
				}
			case info.Mode == "interactive" && !detach:
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

// orLabel resolves a move field: an explicitly-set flag wins; otherwise the
// source pod's label (from PodInfo); otherwise the flag's default. This is what
// lets `poddle move X --size strong` preserve image/identity/harness while
// changing only the size — with no cwd or template.
func orLabel(cmd *cobra.Command, flag, flagVal, label string) string {
	if cmd.Flags().Changed(flag) {
		return flagVal
	}
	if label != "" {
		return label
	}
	return flagVal
}
