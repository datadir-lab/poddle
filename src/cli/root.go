package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"git.dev.datadir.co/datadir/poddle/src/cli/attach"
	"git.dev.datadir.co/datadir/poddle/src/cli/connect"
	"git.dev.datadir.co/datadir/poddle/src/cli/daemon"
	"git.dev.datadir.co/datadir/poddle/src/cli/down"
	cliidentity "git.dev.datadir.co/datadir/poddle/src/cli/identity"
	"git.dev.datadir.co/datadir/poddle/src/cli/ls"
	"git.dev.datadir.co/datadir/poddle/src/cli/run"
	"git.dev.datadir.co/datadir/poddle/src/cli/up"
	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/config"
	"git.dev.datadir.co/datadir/poddle/src/internal/connector"
	"git.dev.datadir.co/datadir/poddle/src/internal/engine"
	"git.dev.datadir.co/datadir/poddle/src/internal/exec"
	"git.dev.datadir.co/datadir/poddle/src/internal/harness"
	"git.dev.datadir.co/datadir/poddle/src/internal/harness/claudecode"
	idn "git.dev.datadir.co/datadir/poddle/src/internal/identity"
	"git.dev.datadir.co/datadir/poddle/src/internal/identity/anthropic"
	"git.dev.datadir.co/datadir/poddle/src/internal/poddled"
	"git.dev.datadir.co/datadir/poddle/src/internal/podman"
	"git.dev.datadir.co/datadir/poddle/src/internal/prompt"
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
