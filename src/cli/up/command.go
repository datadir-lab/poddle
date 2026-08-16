// Package up implements `poddle up`: create a sandbox and connect to it.
package up

import (
	"fmt"
	"net"
	"sort"
	"strings"

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

// podBrokerHost is how a pod addresses the host from inside a podman/docker
// container. It's a container-runtime detail; when a second engine (k8s) lands
// it graduates to an engine capability.
const podBrokerHost = "host.containers.internal"

// podBroker is the persistent-broker capability `up` needs: ensure poddled is
// running, learn its pod-facing gateway address, and issue a pod-scoped handle
// for a credential. Handles live until `down` revokes them (not until up exits),
// so a detached pod keeps working. The real *poddled.Client satisfies it; tests
// pass a spy.
type podBroker interface {
	EnsureRunning() error
	Gateway() (string, error)
	RedisAddr() (string, error)
	IssueHandle(pod, scope string, cred broker.Credential) (string, error)
}

// NewCmd builds the up command. --identity resolves an auth provider; --harness
// resolves a pod-side runtime. b talks to the persistent poddled broker.
func NewCmd(a *app.App, b podBroker) *cobra.Command {
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
			// Warn (or, with secret_scan="block", refuse) when a mounted dir
			// carries credential files. The repo path is a clone, so uncommitted
			// secrets are already absent; this catches explicit bind mounts.
			if findings := secure.ScanMounts(spec.Mounts); len(findings) > 0 {
				mode := tpl.SecretScan
				if mode == "" {
					mode = "warn"
				}
				if mode != "off" {
					var b strings.Builder
					for _, f := range findings {
						fmt.Fprintf(&b, "  - %s (%s)\n", f.Path, f.Rule)
					}
					if mode == "block" {
						return fmt.Errorf("secret_scan: credential files inside mounts (set secret_scan=\"warn\" to allow):\n%s", b.String())
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "poddle: warning — credential files inside bind mounts:\n%s", b.String())
				}
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

			// Any brokered credential — an identity and/or connectors — is issued
			// against the persistent poddled broker. The handles live until
			// `down` revokes them, so the pod (attached or detached) keeps
			// working. poddled auto-starts on first use.
			if identityName != "" || len(tpl.Connectors) > 0 {
				if err := b.EnsureRunning(); err != nil {
					return fmt.Errorf("start poddled: %w", err)
				}
				addr, err := b.Gateway()
				if err != nil {
					return fmt.Errorf("broker gateway: %w", err)
				}
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					return fmt.Errorf("broker address %q: %w", addr, err)
				}
				podBrokerAddr := net.JoinHostPort(podBrokerHost, port)

				if identityName != "" {
					if err := applyIdentity(b, a.Identities, a.Providers, h, identityName, "http://"+podBrokerAddr, &spec); err != nil {
						return err
					}
				}
				var redisPodAddr string // resolved lazily, only if a datastore needs it
				for _, cn := range tpl.Connectors {
					conn, err := a.Connections.Get(cn)
					if err != nil {
						return fmt.Errorf("connection %q: %w", cn, err)
					}
					def, err := connector.LoadDefinition(a.ConnectorsDir, conn.Connector)
					if err != nil {
						return err
					}
					if def.Transport == "l4-redis" {
						if redisPodAddr == "" {
							raddr, err := b.RedisAddr()
							if err != nil {
								return fmt.Errorf("redis broker: %w", err)
							}
							_, rport, err := net.SplitHostPort(raddr)
							if err != nil {
								return fmt.Errorf("redis address %q: %w", raddr, err)
							}
							redisPodAddr = net.JoinHostPort(podBrokerHost, rport)
						}
						if err := applyRedisDatastore(b, conn, def, name, redisPodAddr, &spec); err != nil {
							return err
						}
						continue
					}
					if err := applyConnector(b, conn, def, name, podBrokerAddr, &spec); err != nil {
						return err
					}
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
// Credential, issue a pod-scoped handle for it at poddled, and fold ONLY the
// harness's broker-pointing env + install commands into the spec. The real
// secret never touches the pod.
func applyIdentity(b podBroker, store *idn.Store, reg idn.Registry, h harness.Harness, name, podBrokerURL string, spec *sandbox.Spec) error {
	id, err := store.Get(name)
	if err != nil {
		return fmt.Errorf("identity %q: %w", name, err)
	}
	p, ok := reg.Get(id.Provider)
	if !ok {
		return fmt.Errorf("unknown provider %q for identity %q", id.Provider, name)
	}
	authed, err := p.IsAuthenticated(id)
	if err != nil {
		return err
	}
	if !authed {
		if err := p.Authenticate(id); err != nil {
			return fmt.Errorf("authenticate %q: %w", name, err)
		}
	}
	cred, err := p.Credential(id)
	if err != nil {
		return err
	}
	if !h.Supports(cred.Vendor) {
		return fmt.Errorf("harness %q does not support vendor %q", h.Name(), cred.Vendor)
	}

	handle, err := b.IssueHandle(spec.Name, spec.Name, cred) // scope = pod name
	if err != nil {
		return err
	}

	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	for k, v := range h.Env(podBrokerURL, handle) {
		spec.Env[k] = v
	}
	spec.Setup = append(spec.Setup, h.Provisions()...)
	return nil
}

// applyConnector issues a pod-scoped handle for a connection's service token at
// poddled and folds the connector's pod wiring (env + setup) into the spec.
// Connector setup (e.g. git url.insteadOf) is PREPENDED so it runs before the
// repo clone. The real token never enters the pod.
func applyConnector(b podBroker, conn connector.Connection, def connector.Definition, podName, podBrokerAddr string, spec *sandbox.Spec) error {
	cred, err := connector.Credential(conn, def)
	if err != nil {
		return err
	}
	handle, err := b.IssueHandle(podName, podName+"/"+conn.Name, cred)
	if err != nil {
		return err
	}
	env, setup := connector.Wiring(def, cred, podBrokerAddr, handle)
	if len(env) > 0 {
		if spec.Env == nil {
			spec.Env = map[string]string{}
		}
		for k, v := range env {
			spec.Env[k] = v
		}
	}
	spec.Setup = append(setup, spec.Setup...) // connector setup before the clone
	return nil
}

// applyRedisDatastore issues a handle for a Redis connection at poddled and
// points the pod at the broker's L4 Redis address with the handle as its
// password. The real DSN (user+password) stays in the broker.
func applyRedisDatastore(b podBroker, conn connector.Connection, def connector.Definition, podName, redisPodAddr string, spec *sandbox.Spec) error {
	cred, err := connector.Credential(conn, def)
	if err != nil {
		return err
	}
	handle, err := b.IssueHandle(podName, podName+"/"+conn.Name, cred)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(redisPodAddr)
	if err != nil {
		return err
	}
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	spec.Env["REDIS_HOST"] = host
	spec.Env["REDIS_PORT"] = port
	spec.Env["REDIS_PASSWORD"] = handle
	spec.Env["REDIS_URL"] = "redis://:" + handle + "@" + redisPodAddr
	return nil
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
