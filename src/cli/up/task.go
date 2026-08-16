package up

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
)

// NewTaskCmd builds `poddle task`: create a fresh secretless pod, run the coding
// agent headless on the prompt to completion, then tear the pod down (revoke its
// handles + remove the container) unless --keep. It reuses up's buildSpec, so a
// task pod gets the same identity/connectors/harness/secret-safety as `up`.
func NewTaskCmd(a *app.App, b podBroker) *cobra.Command {
	var image, size, identityName, harnessName, templateName, podName string
	var maxTurns int
	var keep bool

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
			spec, h, err := buildSpec(cmd, a, b, buildOpts{
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
			if !keep {
				defer func() {
					_ = b.RevokePod(name)     // best-effort: kill the pod's handles
					_ = a.Engine.Remove(name) // then the container
				}()
			}
			return a.Engine.Exec(id, taskCmd)
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
	return c
}
