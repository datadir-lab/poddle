// Package up implements `poddle up`: create a sandbox and connect to it.
package up

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"git.dev.datadir.co/datadir/poddle/src/internal/app"
	"git.dev.datadir.co/datadir/poddle/src/internal/broker"
	"git.dev.datadir.co/datadir/poddle/src/internal/config"
	"git.dev.datadir.co/datadir/poddle/src/internal/connector"
	"git.dev.datadir.co/datadir/poddle/src/internal/harness"
	idn "git.dev.datadir.co/datadir/poddle/src/internal/identity"
	"git.dev.datadir.co/datadir/poddle/src/internal/sandbox"
	"git.dev.datadir.co/datadir/poddle/src/internal/secure"
)

// defaultPodBrokerHost is how a pod addresses the host running the broker from
// inside a local podman/docker container.
const defaultPodBrokerHost = "host.containers.internal"

// podBrokerHost returns the address a pod uses to reach the broker. For local
// pods that's host.containers.internal; for a remote pod (on another host) the
// broker isn't local to it, so PODDLE_BROKER_ADDR sets the routable address the
// remote pod should dial (e.g. this machine's LAN IP).
func podBrokerHost() string {
	if a := os.Getenv("PODDLE_BROKER_ADDR"); a != "" {
		return a
	}
	return defaultPodBrokerHost
}

// podBroker is the persistent-broker capability `up` needs: ensure poddled is
// running, learn its pod-facing gateway address, and issue a pod-scoped handle
// for a credential. Handles live until `down` revokes them (not until up exits),
// so a detached pod keeps working. The real *poddled.Client satisfies it; tests
// pass a spy.
type podBroker interface {
	EnsureRunning() error
	Gateway() (string, error)
	RedisAddr() (string, error)
	PostgresAddr() (string, error)
	IssueHandle(pod, scope string, cred broker.Credential) (string, error)
	RevokePod(pod string) error
}

// NewCmd builds the up command. --identity resolves an auth provider; --harness
// resolves a pod-side runtime. b talks to the persistent poddled broker.
func NewCmd(a *app.App, b podBroker) *cobra.Command {
	var image, size, identityName, harnessName, execCmd, templateName string
	var detach, autoscale bool

	c := &cobra.Command{
		Use:   "up [name]",
		Short: "Create a sandbox and connect to it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "poddle"
			if len(args) > 0 {
				name = args[0]
			}
			spec, _, _, err := buildSpec(cmd, a, b, buildOpts{
				name: name, image: image, size: size, identityName: identityName,
				harnessName: harnessName, templateName: templateName,
				allowSelect: !detach && execCmd == "" && a.Prompter != nil,
				withVolumes: true, // stateful session (workspace + agent state)
				autoscale:   autoscale,
			})
			if err != nil {
				return err
			}
			spec.Mode = "interactive"
			if execCmd != "" {
				spec.Mode = "exec" // one-shot, nothing to resume on move
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
	c.Flags().BoolVar(&autoscale, "autoscale", false, "let poddled warn when this pod nears its memory limit (interactive: warn only)")
	return c
}

// buildOpts carries the resolved flag values buildSpec needs.
type buildOpts struct {
	name, image, size, identityName, harnessName, templateName string
	allowSelect                                                bool // interactively select an identity when none is given and a Prompter exists
	requireIdentity                                            bool // error if no identity is resolved (an autonomous task needs auth)
	withVolumes                                                bool // mount session state on named volumes (up/move; not ephemeral task pods)
	skipClone                                                  bool // don't clone the repo (move: the workspace volume already has it)
	autoscale                                                  bool // opt in to the daemon's reactive memory-grow autoscaler
}

// stateVolName is the deterministic volume name for a pod's harness state dir,
// so `move` reuses the same volume the pod was created with.
func stateVolName(pod, dir string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, strings.Trim(dir, "/"))
	return "poddle-" + pod + "-" + s
}

// buildSpec resolves the template + flag overrides, runs secret-safety, and
// issues broker handles (identity + connectors) against poddled — returning a
// ready-to-create spec and the resolved harness. It does not create the pod, so
// both `up` and `task` share it.
func buildSpec(cmd *cobra.Command, a *app.App, b podBroker, o buildOpts) (sandbox.Spec, harness.Harness, config.Template, error) {
	fail := func(err error) (sandbox.Spec, harness.Harness, config.Template, error) {
		return sandbox.Spec{}, nil, config.Template{}, err
	}

	var tpl config.Template
	if a.Templates != nil {
		var err error
		if tpl, err = a.Templates.Resolve(o.templateName); err != nil {
			return fail(err)
		}
	}
	harnessName := flagOr(cmd, "harness", o.harnessName, tpl.Harness)
	image := flagOr(cmd, "image", o.image, tpl.Image)
	size := flagOr(cmd, "size", o.size, tpl.Size)
	identityName := o.identityName
	if identityName == "" && tpl.Identity != "" {
		identityName = tpl.Identity
	}

	h, ok := a.Harnesses.Get(harnessName)
	if !ok {
		return fail(fmt.Errorf("unknown harness %q", harnessName))
	}
	cpus, mem := resolveSize(size)
	spec := sandbox.Spec{
		Name: o.name, Image: image, Template: "base",
		Runtime: "container", Size: size, CPUs: cpus, Memory: mem, Repo: tpl.Repo,
		Autoscale: o.autoscale || tpl.Autoscale, // opt in via flag or template
		Harness:   harnessName,                  // labelled so `move` recreates with the same runtime
	}

	// Session state on named volumes: /workspace + the harness's state dirs. They
	// survive `poddle move` and are removed by `poddle down`. Ephemeral task pods
	// opt out (withVolumes=false).
	if o.withVolumes {
		spec.Volumes = append(spec.Volumes, sandbox.Volume{Name: "poddle-" + o.name + "-workspace", Container: "/workspace"})
		for _, dir := range h.StateDirs() {
			spec.Volumes = append(spec.Volumes, sandbox.Volume{Name: stateVolName(o.name, dir), Container: dir})
		}
	}

	if len(tpl.Env) > 0 {
		spec.Env = map[string]string{}
		for k, v := range tpl.Env {
			spec.Env[k] = v
		}
	}
	for _, m := range tpl.Mounts {
		spec.Mounts = append(spec.Mounts, sandbox.Mount{Host: m.Host, Container: m.Container, ReadOnly: m.ReadOnly})
	}
	if err := secure.CheckMounts(spec.Mounts, tpl.BlockPaths); err != nil {
		return fail(err)
	}
	if findings := secure.ScanMounts(spec.Mounts); len(findings) > 0 {
		mode := tpl.SecretScan
		if mode == "" {
			mode = "warn"
		}
		if mode != "off" {
			var sb strings.Builder
			for _, f := range findings {
				fmt.Fprintf(&sb, "  - %s (%s)\n", f.Path, f.Rule)
			}
			if mode == "block" {
				return fail(fmt.Errorf("secret_scan: credential files inside mounts (set secret_scan=\"warn\" to allow):\n%s", sb.String()))
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "poddle: warning — credential files inside bind mounts:\n%s", sb.String())
		}
	}
	setupCmds, err := tpl.SetupCommands()
	if err != nil {
		return fail(err)
	}
	if tpl.Repo != "" && !o.skipClone {
		setupCmds = append([]string{"git clone " + tpl.Repo + " /workspace"}, setupCmds...)
	}
	spec.Setup = append(spec.Setup, setupCmds...)

	if identityName == "" && o.allowSelect && a.Prompter != nil {
		chosen, err := selectIdentity(a)
		if err != nil {
			return fail(err)
		}
		identityName = chosen
	}
	if o.requireIdentity && identityName == "" {
		return fail(fmt.Errorf("no identity: pass --identity or set one in the template"))
	}
	spec.Identity = identityName // labelled so `move` can re-broker the same login

	// Any brokered credential — an identity and/or connectors — is issued
	// against the persistent poddled broker; the handles live until `down`.
	if identityName != "" || len(tpl.Connectors) > 0 {
		if err := b.EnsureRunning(); err != nil {
			return fail(fmt.Errorf("start poddled: %w", err))
		}
		addr, err := b.Gateway()
		if err != nil {
			return fail(fmt.Errorf("broker gateway: %w", err))
		}
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return fail(fmt.Errorf("broker address %q: %w", addr, err))
		}
		podBrokerAddr := net.JoinHostPort(podBrokerHost(), port)

		if identityName != "" {
			if err := applyIdentity(b, a.Identities, a.Providers, h, identityName, "http://"+podBrokerAddr, &spec); err != nil {
				return fail(err)
			}
		}
		var redisPodAddr, pgPodAddr string // resolved lazily, only if needed
		for _, cn := range tpl.Connectors {
			conn, err := a.Connections.Get(cn)
			if err != nil {
				return fail(fmt.Errorf("connection %q: %w", cn, err))
			}
			def, err := connector.LoadDefinition(a.ConnectorsDir, conn.Connector)
			if err != nil {
				return fail(err)
			}
			switch def.Transport {
			case "l4-redis":
				if redisPodAddr, err = podL4Addr(redisPodAddr, b.RedisAddr); err != nil {
					return fail(err)
				}
				if err := applyRedisDatastore(b, conn, def, o.name, redisPodAddr, &spec); err != nil {
					return fail(err)
				}
			case "l4-postgres":
				if pgPodAddr, err = podL4Addr(pgPodAddr, b.PostgresAddr); err != nil {
					return fail(err)
				}
				if err := applyPostgresDatastore(b, conn, def, o.name, pgPodAddr, &spec); err != nil {
					return fail(err)
				}
			default:
				if err := applyConnector(b, conn, def, o.name, podBrokerAddr, &spec); err != nil {
					return fail(err)
				}
			}
		}
	}
	return spec, h, tpl, nil
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

// applyPostgresDatastore issues a handle for a Postgres connection at poddled and
// points the pod at the broker's L4 Postgres address with the handle as its
// password (PGHOST/PGPORT/PGUSER/PGPASSWORD/PGDATABASE). The real DSN stays in
// the broker.
func applyPostgresDatastore(b podBroker, conn connector.Connection, def connector.Definition, podName, pgPodAddr string, spec *sandbox.Spec) error {
	cred, err := connector.Credential(conn, def)
	if err != nil {
		return err
	}
	handle, err := b.IssueHandle(podName, podName+"/"+conn.Name, cred)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(pgPodAddr)
	if err != nil {
		return err
	}
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	spec.Env["PGHOST"] = host
	spec.Env["PGPORT"] = port
	spec.Env["PGPASSWORD"] = handle
	if conn.User != "" {
		spec.Env["PGUSER"] = conn.User
	}
	base := conn.BaseURL
	if !strings.Contains(base, "://") {
		base = "postgres://" + base
	}
	if u, err := url.Parse(base); err == nil {
		if db := strings.TrimPrefix(u.Path, "/"); db != "" {
			spec.Env["PGDATABASE"] = db
		}
	}
	return nil
}

// podL4Addr returns cached if set, else fetches the daemon's L4 address and
// rewrites its host to how a pod reaches the broker.
func podL4Addr(cached string, fetch func() (string, error)) (string, error) {
	if cached != "" {
		return cached, nil
	}
	addr, err := fetch()
	if err != nil {
		return "", fmt.Errorf("l4 broker: %w", err)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("l4 address %q: %w", addr, err)
	}
	return net.JoinHostPort(podBrokerHost(), port), nil
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
