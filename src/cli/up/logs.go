package up

import (
	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
)

// NewLogsCmd builds `poddle logs <pod>`: show a detached task's output (the log
// `poddle task --detach` writes inside the pod). --follow streams it.
func NewLogsCmd(a *app.App) *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use:   "logs <name|id>",
		Short: "Show a detached task's output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reader := "cat " + TaskLogPath
			if follow {
				reader = "tail -f " + TaskLogPath
			}
			return a.Engine.Exec(args[0], reader+" 2>/dev/null || echo '(no task output yet)'")
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "stream the log (tail -f)")
	return c
}
