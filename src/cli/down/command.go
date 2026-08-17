// Package down implements `poddle down`: stop and remove a sandbox.
package down

import (
	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
)

// podRevoker revokes the handles poddled issued for a pod. The real
// *poddled.Client satisfies it; tests pass a spy.
type podRevoker interface {
	RevokePod(pod string) error
}

// NewCmd builds the down command. It revokes the pod's brokered handles at
// poddled (best-effort — the daemon may be down, or the pod had no creds) before
// removing the container.
func NewCmd(a *app.App, r podRevoker) *cobra.Command {
	return &cobra.Command{
		Use:   "down <name|id>",
		Short: "Stop and remove a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = r.RevokePod(args[0]) // best-effort: the pod's handles die with it
			if err := a.Engine.Remove(args[0]); err != nil {
				return err
			}
			return a.Engine.RemoveVolumesForPod(args[0]) // the session's state is gone
		},
	}
}
