package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/cli/down"
	cliidentity "github.com/datadir-lab/poddle/src/cli/identity"
	"github.com/datadir-lab/poddle/src/cli/ls"
	"github.com/datadir-lab/poddle/src/cli/up"
	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/engine"
	"github.com/datadir-lab/poddle/src/internal/exec"
	"github.com/datadir-lab/poddle/src/internal/harness"
	"github.com/datadir-lab/poddle/src/internal/harness/claudecode"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
	"github.com/datadir-lab/poddle/src/internal/identity/anthropic"
	"github.com/datadir-lab/poddle/src/internal/podman"
)

// NewRootCmd builds the root poddle command and registers the feature slices.
// It is the composition root: the engine, identity store, and provider/harness
// registries are constructed once, bundled into an app.App, and injected into
// each command. The secretless broker is up-scoped (Phase 1), so it is passed
// only to `up`.
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

	root.AddCommand(ls.NewCmd(a))
	root.AddCommand(up.NewCmd(a, broker.NewBroker())) // broker is up-scoped (Phase 1)
	root.AddCommand(down.NewCmd(a))
	root.AddCommand(cliidentity.NewCmd(a))
	return root
}

// Execute builds and runs the root command.
func Execute() error {
	return NewRootCmd().Execute()
}
