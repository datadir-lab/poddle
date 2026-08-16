package up

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
)

// TaskLogPath is where a detached task's output is written inside the pod;
// `poddle logs` reads it back.
const TaskLogPath = "/tmp/poddle-task.json"

// NewTaskCmd builds `poddle task`: create a fresh secretless pod, run the coding
// agent headless on the prompt, then tear the pod down (revoke its handles +
// remove the container) unless --keep. With --detach the agent runs in the
// background (output to TaskLogPath) and the pod is left up for `poddle
// logs`/`attach`/`down`. It reuses up's buildSpec, so a task pod gets the same
// identity/connectors/harness/secret-safety as `up`.
func NewTaskCmd(a *app.App, b podBroker) *cobra.Command {
	var image, size, identityName, harnessName, templateName, podName string
	var maxTurns int
	var keep, detach bool

	c := &cobra.Command{
		Use:   "task <prompt>",
		Short: "Run a coding agent headless on a prompt in a fresh secretless pod",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := args[0]
			name := podName
			if name == "" {
				name = "poddle-task-" + strconv.FormatInt(time.Now().UnixNano(), 36)
			}
			spec, h, tpl, err := buildSpec(cmd, a, b, buildOpts{
				name: name, image: image, size: size, identityName: identityName,
				harnessName: harnessName, templateName: templateName,
				requireIdentity: true, // an autonomous agent needs an LLM login
			})
			if err != nil {
				return err
			}
			taskCmd := h.TaskCommand(prompt, maxTurns)
			if taskCmd == "" {
				return fmt.Errorf("harness %q has no headless task mode", h.Name())
			}

			id, err := a.Engine.Create(spec)
			if err != nil {
				return err
			}

			// before_task hook: burst the pod up for the run.
			if tpl.BeforeTask != "" {
				c, m := resolveSize(tpl.BeforeTask)
				_ = a.Engine.Resize(name, c, m)
			}

			if detach {
				// Run the agent in the background, its output to a log the pod
				// keeps; leave the pod up for `poddle logs`/`attach`/`down`.
				// (after_task can't fire — the CLI is gone when the agent ends.)
				wrapped := "( " + taskCmd + " ) > " + TaskLogPath + " 2>&1"
				if err := a.Engine.ExecDetached(id, wrapped); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), name)
				fmt.Fprintf(cmd.ErrOrStderr(),
					"task running in %q — `poddle logs %s` to watch, `poddle down %s` when done\n", name, name, name)
				return nil
			}

			runErr := a.Engine.Exec(id, taskCmd)
			if keep {
				// after_task hook: drop the (kept) pod back down.
				if tpl.AfterTask != "" {
					c, m := resolveSize(tpl.AfterTask)
					_ = a.Engine.Resize(name, c, m)
				}
			} else {
				_ = b.RevokePod(name)     // best-effort: kill the pod's handles
				_ = a.Engine.Remove(name) // then the container
			}
			return runErr
		},
	}
	c.Flags().StringVar(&image, "image", "docker.io/library/debian:stable-slim", "base image")
	c.Flags().StringVar(&size, "size", "weak", "resource size (weak|strong)")
	c.Flags().StringVar(&identityName, "identity", "", "coding-agent login to use (required unless the template sets one)")
	c.Flags().StringVar(&harnessName, "harness", "claude-code", "coding-agent runtime")
	c.Flags().StringVar(&templateName, "template", "", "template to base the pod on")
	c.Flags().StringVar(&podName, "name", "", "pod name (default: a generated poddle-task-* name)")
	c.Flags().IntVar(&maxTurns, "max-turns", 24, "maximum agent turns")
	c.Flags().BoolVar(&keep, "keep", false, "keep the pod running after the task (attach/inspect later)")
	c.Flags().BoolVarP(&detach, "detach", "d", false, "run the agent in the background; leave the pod up (poddle logs/down)")
	return c
}
