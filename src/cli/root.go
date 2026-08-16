package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/datadir-lab/poddle/src/cli/attach"
	"github.com/datadir-lab/poddle/src/cli/connect"
	"github.com/datadir-lab/poddle/src/cli/daemon"
	"github.com/datadir-lab/poddle/src/cli/down"
	cliidentity "github.com/datadir-lab/poddle/src/cli/identity"
	"github.com/datadir-lab/poddle/src/cli/ls"
	"github.com/datadir-lab/poddle/src/cli/run"
	"github.com/datadir-lab/poddle/src/cli/up"
	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/config"
	"github.com/datadir-lab/poddle/src/internal/connector"
	"github.com/datadir-lab/poddle/src/internal/engine"
	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/harness"
	"github.com/datadir-lab/poddle/src/internal/harness/claudecode"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
	"github.com/datadir-lab/poddle/src/internal/identity/anthropic"
	"github.com/datadir-lab/poddle/src/internal/poddled"
	"github.com/datadir-lab/poddle/src/internal/podman"
	"github.com/datadir-lab/poddle/src/internal/prompt"
)

// NewRootCmd builds the root poddle command and registers the feature slices.
// It is the composition root: the engine, identity store, and provider/harness
// registries are constructed once, bundled into an app.App, and injected into
// each command. The secretless broker now lives in poddled (auto-started); a
// poddled client is passed to `up` (issue handles) and `down` (revoke them).
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "poddle",
		Short:        "poddle — self-hostable, secret-safe agent dev environments",
		SilenceUsage: true,
	}

	// PODDLE_HOST empty = local podman; ssh://… = a remote host (same code path).
	var eng engine.Engine = podman.New(exec.OS{}, os.Getenv("PODDLE_HOST"))

	// Identities live on the client (never only in poddle). Providers are the
	// auth vendors, vertically sliced.
	store := idn.NewStore(idn.DefaultBase())
	reg := idn.Registry{
		"anthropic": anthropic.New(),
	}

	// Harnesses are the pod-side coding-agent runtimes (--harness).
	harnesses := harness.Registry{
		"claude-code": claudecode.New(),
	}

	// The composition root: one App, injected into every command.
	a := &app.App{
		Engine:     eng,
		Identities: store,
		Providers:  reg,
		Harnesses:  harnesses,
	}
	// Interactive prompts only on a real terminal; scripts/CI get no Prompter.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		a.Prompter = prompt.NewHuh()
	}

	// Templates: user blueprints + the project's .poddle/ (loaded lazily per up).
	cwd, _ := os.Getwd()
	userCfg, _ := os.UserConfigDir()
	a.Templates = config.DirResolver{
		UserDir:    filepath.Join(userCfg, "poddle", "templates"),
		ProjectDir: cwd,
	}

	// Service connections (git/CI/…) + user connector definitions.
	a.Connections = connector.NewStore(connector.DefaultBase())
	a.ConnectorsDir = filepath.Join(userCfg, "poddle", "connectors")

	root.AddCommand(ls.NewCmd(a))
	root.AddCommand(up.NewCmd(a, poddled.NewClient(""))) // persistent broker (auto-started)
	root.AddCommand(up.NewTaskCmd(a, poddled.NewClient("")))
	root.AddCommand(attach.NewCmd(a))
	root.AddCommand(run.NewCmd(a))
	root.AddCommand(up.NewLogsCmd(a))
	root.AddCommand(down.NewCmd(a, poddled.NewClient("")))
	root.AddCommand(cliidentity.NewCmd(a))
	root.AddCommand(connect.NewCmd(a))
	root.AddCommand(daemon.NewCmd())
	return root
}

// Execute builds and runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
