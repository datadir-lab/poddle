// Package up implements `poddle up`: create a sandbox and connect to it.
package up

import (
	"context"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/config"
	"github.com/datadir-lab/poddle/src/internal/connector"
	"github.com/datadir-lab/poddle/src/internal/harness"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
	"github.com/datadir-lab/poddle/src/internal/secure"
)

// brokerBindAddr is where the up-scoped broker listens in Phase 1: all
// interfaces on an ephemeral port, so the pod's container can reach it via the
// podman bridge. It is handle-gated (401 without a valid handle); Phase 2 binds
// tighter (bridge-only / unix socket / tunnel).
const brokerBindAddr = "0.0.0.0:0"

// podBrokerHost is how a pod addresses the host from inside a podman/docker
// container. It's a container-runtime detail; when a second engine (k8s) lands
// it graduates to an engine capability.
const podBrokerHost = "host.containers.internal"

// credBroker is the broker capability `up` needs. The real *broker.Broker
// satisfies it; tests pass a spy. (Named to avoid clashing with the broker
// package import.)
type credBroker interface {
	Serve(addr string) (string, error)
	Addr() string
	Store(broker.Credential) (string, error)
	IssueHandle(credID, scope string, ttl time.Duration) (broker.Handle, error)
	Revoke(handleValue string)
	Stop(ctx context.Context) error
}

// NewCmd builds the up command. --identity resolves an auth provider; --harness
// resolves a pod-side runtime. b is the up-scoped secretless broker.
func NewCmd(a *app.App, b credBroker) *cobra.Command {
	var image, size, identityName, harnessName, execCmd, templateName string
	var detach bool

	c := &cobra.Command{
		Use:   "up [name]",
		Short: "Create a sandbox and connect to it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve the template (project default, or --template), then let
			// explicit CLI flags override it.
			var tpl config.Template
			if a.Templates != nil {
				var err error
				if tpl, err = a.Templates.Resolve(templateName); err != nil {
					return err
				}
			}
			harnessName = flagOr(cmd, "harness", harnessName, tpl.Harness)
			image = flagOr(cmd, "image", image, tpl.Image)
			size = flagOr(cmd, "size", size, tpl.Size)
			if identityName == "" && tpl.Identity != "" {
				identityName = tpl.Identity
			}

			h, ok := a.Harnesses.Get(harnessName)
			if !ok {
				return fmt.Errorf("unknown harness %q", harnessName)
			}
			name := "poddle"
			if len(args) > 0 {
				name = args[0]
			}
			cpus, mem := resolveSize(size)
			spec := sandbox.Spec{
				Name: name, Image: image, Template: "base",
				Runtime: "container", Size: size, CPUs: cpus, Memory: mem, Repo: tpl.Repo,
			}

			// Fold the template's env, mounts, and setup (inline + scripts) in.
			if len(tpl.Env) > 0 {
				spec.Env = map[string]string{}
				for k, v := range tpl.Env {
					spec.Env[k] = v
				}
			}
			for _, m := range tpl.Mounts {
				spec.Mounts = append(spec.Mounts, sandbox.Mount{Host: m.Host, Container: m.Container, ReadOnly: m.ReadOnly})
			}
			// Secret-safety: refuse mounts that would expose host secrets (the
			// always-on deny-list plus the template's block_paths).
			if err := secure.CheckMounts(spec.Mounts, tpl.BlockPaths); err != nil {
				return err
			}
			setupCmds, err := tpl.SetupCommands()
			if err != nil {
				return err
			}
			// A template repo is cloned into /workspace first, before setup runs.
			// (Private-repo auth arrives with broker'd git tokens; public or
			// token-in-URL works today. The image must have git.)
			if tpl.Repo != "" {
				setupCmds = append([]string{"git clone " + tpl.Repo + " /workspace"}, setupCmds...)
			}
			spec.Setup = append(spec.Setup, setupCmds...)

			// No --identity on an interactive TTY: let the user pick one (or add
			// one, or a plain sandbox). Scripts/CI (no Prompter), --detach, and
			// --exec skip this.
			if identityName == "" && !detach && execCmd == "" && a.Prompter != nil {
				chosen, err := selectIdentity(a)
				if err != nil {
					return err
				}
				identityName = chosen
			}

			// The broker serves when there's any brokered credential — an
			// identity and/or connectors — and is torn down when the session ends.
			if identityName != "" || len(tpl.Connectors) > 0 {
				if detach {
					return fmt.Errorf("--detach with an identity or connector needs poddled (Phase 2); attach to keep the broker alive")
				}
				addr, err := b.Serve(brokerBindAddr)
				if err != nil {
					return fmt.Errorf("serve broker: %w", err)
				}
				defer func() { _ = b.Stop(context.Background()) }()
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					return fmt.Errorf("broker address %q: %w", addr, err)
				}
				podBrokerAddr := net.JoinHostPort(podBrokerHost, port)

				if identityName != "" {
					handle, err := applyIdentity(b, a.Identities, a.Providers, h, identityName, "http://"+podBrokerAddr, &spec)
					if err != nil {
						return err
					}
					defer b.Revoke(handle)
				}
				for _, cn := range tpl.Connectors {
					handle, err := applyConnector(b, a.Connections, a.ConnectorsDir, cn, name, podBrokerAddr, &spec)
					if err != nil {
						return err
					}
					defer b.Revoke(handle)
				}
			}

			id, err := a.Engine.Create(spec)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), id)

			if execCmd != "" {
				return a.Engine.Exec(id, execCmd)
			}
			if detach {
				return nil
			}
			return a.Engine.Attach(id)
		},
	}
	c.Flags().StringVar(&image, "image", "docker.io/library/debian:stable-slim", "base image")
	c.Flags().StringVar(&size, "size", "weak", "resource size (weak|strong)")
	c.Flags().StringVar(&identityName, "identity", "", "coding-agent login to use in the sandbox")
	c.Flags().StringVar(&harnessName, "harness", "claude-code", "coding-agent runtime to run in the sandbox")
	c.Flags().StringVar(&templateName, "template", "", "template to base the sandbox on (from .poddle/ or ~/.config/poddle/templates)")
	c.Flags().StringVar(&execCmd, "exec", "", "run a command in the sandbox instead of attaching, then tear down")
	c.Flags().BoolVarP(&detach, "detach", "d", false, "create without attaching")
	return c
}

// applyIdentity wires an identity into the pod secretlessly: re-authenticate on
// the client if stale (never a dead cred into the pod), take the real
// Credential, seal it in the broker, and fold ONLY the harness's broker-pointing
// env + install commands into the spec. Returns the issued handle so the caller
// can revoke it on teardown. The real secret never touches the pod.
func applyIdentity(b credBroker, store *idn.Store, reg idn.Registry, h harness.Harness, name, podBrokerURL string, spec *sandbox.Spec) (string, error) {
	id, err := store.Get(name)
	if err != nil {
		return "", fmt.Errorf("identity %q: %w", name, err)
	}
	p, ok := reg.Get(id.Provider)
	if !ok {
		return "", fmt.Errorf("unknown provider %q for identity %q", id.Provider, name)
	}
	authed, err := p.IsAuthenticated(id)
	if err != nil {
		return "", err
	}
	if !authed {
		if err := p.Authenticate(id); err != nil {
			return "", fmt.Errorf("authenticate %q: %w", name, err)
		}
	}
	cred, err := p.Credential(id)
	if err != nil {
		return "", err
	}
	if !h.Supports(cred.Vendor) {
		return "", fmt.Errorf("harness %q does not support vendor %q", h.Name(), cred.Vendor)
	}

	credID, err := b.Store(cred)
	if err != nil {
		return "", err
	}
	handle, err := b.IssueHandle(credID, spec.Name, 0) // 0 → DefaultHandleTTL, scope = pod name
	if err != nil {
		return "", err
	}

	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	for k, v := range h.Env(podBrokerURL, handle.Value) {
		spec.Env[k] = v
	}
	spec.Setup = append(spec.Setup, h.Provisions()...)
	return handle.Value, nil
}

// applyConnector seals a connection's service token in the broker, issues a
// handle, and folds the connector's pod wiring (env + setup) into the spec.
// Connector setup (e.g. git url.insteadOf) is PREPENDED so it runs before the
// repo clone. The real token never enters the pod.
func applyConnector(b credBroker, store *connector.Store, connectorsDir, connName, podName, podBrokerAddr string, spec *sandbox.Spec) (string, error) {
	conn, err := store.Get(connName)
	if err != nil {
		return "", fmt.Errorf("connection %q: %w", connName, err)
	}
	def, err := connector.LoadDefinition(connectorsDir, conn.Connector)
	if err != nil {
		return "", err
	}
	cred, err := connector.Credential(conn, def)
	if err != nil {
		return "", err
	}
	credID, err := b.Store(cred)
	if err != nil {
		return "", err
	}
	handle, err := b.IssueHandle(credID, podName+"/"+connName, 0)
	if err != nil {
		return "", err
	}
	env, setup := connector.Wiring(def, cred, podBrokerAddr, handle.Value)
	if len(env) > 0 {
		if spec.Env == nil {
			spec.Env = map[string]string{}
		}
		for k, v := range env {
			spec.Env[k] = v
		}
	}
	spec.Setup = append(setup, spec.Setup...) // connector setup before the clone
	return handle.Value, nil
}

const (
	optAddIdentity = "➕ Add a new identity"
	optNoIdentity  = "None — plain sandbox"
)

// selectIdentity interactively picks an identity when --identity was omitted.
// Returns the chosen identity name, or "" for a plain (no-identity) sandbox.
func selectIdentity(a *app.App) (string, error) {
	ids, err := a.Identities.List()
	if err != nil {
		return "", err
	}
	options := make([]string, 0, len(ids)+2)
	for _, id := range ids {
		options = append(options, id.Name+" ("+id.Provider+")")
	}
	options = append(options, optAddIdentity, optNoIdentity)

	i, err := a.Prompter.Select("Use which coding-agent login?", options)
	if err != nil {
		return "", err
	}
	switch {
	case i < len(ids):
		return ids[i].Name, nil
	case options[i] == optAddIdentity:
		return addIdentity(a)
	default:
		return "", nil // plain sandbox
	}
}

// addIdentity prompts for a provider + name, runs its auth, and returns the name.
func addIdentity(a *app.App) (string, error) {
	providers := providerNames(a.Providers)
	if len(providers) == 0 {
		return "", fmt.Errorf("no auth providers registered")
	}
	pi, err := a.Prompter.Select("Auth provider?", providers)
	if err != nil {
		return "", err
	}
	providerName := providers[pi]
	name, err := a.Prompter.Input("Name this identity")
	if err != nil {
		return "", err
	}
	p, ok := a.Providers.Get(providerName)
	if !ok {
		return "", fmt.Errorf("unknown provider %q", providerName)
	}
	id, err := a.Identities.Create(name, providerName)
	if err != nil {
		return "", err
	}
	if err := p.Authenticate(id); err != nil {
		return "", fmt.Errorf("authenticate %q: %w", name, err)
	}
	return name, nil
}

func providerNames(reg idn.Registry) []string {
	names := make([]string, 0, len(reg))
	for k := range reg {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// flagOr returns the flag value if the user set it explicitly, else the
// template value when non-empty, else the flag's default.
func flagOr(cmd *cobra.Command, name, flagVal, tplVal string) string {
	if !cmd.Flags().Changed(name) && tplVal != "" {
		return tplVal
	}
	return flagVal
}

// resolveSize maps a size tier to concrete cpu/memory limits.
func resolveSize(size string) (cpus float64, memory string) {
	switch size {
	case "strong":
		return 8, "16g"
	default: // weak
		return 2, "4g"
	}
}
