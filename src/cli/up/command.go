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

	"github.com/datadir-lab/poddle/src/internal/app"
	"github.com/datadir-lab/poddle/src/internal/audit"
	"github.com/datadir-lab/poddle/src/internal/broker"
	"github.com/datadir-lab/poddle/src/internal/brokerendpoint"
	"github.com/datadir-lab/poddle/src/internal/config"
	"github.com/datadir-lab/poddle/src/internal/connector"
	"github.com/datadir-lab/poddle/src/internal/harness"
	"github.com/datadir-lab/poddle/src/internal/harnessconfig"
	idn "github.com/datadir-lab/poddle/src/internal/identity"
	"github.com/datadir-lab/poddle/src/internal/poddled"
	"github.com/datadir-lab/poddle/src/internal/policy"
	"github.com/datadir-lab/poddle/src/internal/sandbox"
	"github.com/datadir-lab/poddle/src/internal/secure"
	"github.com/datadir-lab/poddle/src/internal/tlsca"
)

// egressCAPath is where an intercepted pod finds the poddle egress CA.
const egressCAPath = "/etc/poddle/egress-ca.crt"

// defaultPolicyName labels the derived default-deny policy poddle binds to a
// brokered pod that has no explicit policy — scoped to exactly what the pod
// needs so it is contained, not just audited.
const defaultPolicyName = "poddle-default"

// dedupeHosts drops empty and duplicate hosts, preserving first-seen order.
func dedupeHosts(hosts []string) []string {
	seen := make(map[string]bool, len(hosts))
	var out []string
	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// repoEgressHost returns the host an http(s) `repo` clone egresses to through
// the forward proxy, so a default-deny pod can be allowed to reach it. It returns
// "" for an SSH/scp-style repo (git@host:path) or anything unparseable — those
// don't egress via the HTTP forward proxy, so there is nothing to allow here.
func repoEgressHost(repo string) string {
	u, err := url.Parse(repo)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.Hostname()
}

// injectEgressCA gives an intercepted pod the egress CA in its trust store, so
// the broker can terminate TLS on its HTTPS egress. It mounts the CA cert
// read-only and points the common toolchains (node, python, curl, git) at it via
// env; a Setup step also adds it to the OS trust for anything using the system
// bundle. caDir is the broker's shared CA dir (poddled.EgressCADir()): the broker
// GENERATES and signs with this CA at start (EnsureRunning precedes this call and
// waits for health), so the cert is already on disk — this reads it rather than
// generating a competing one, which is why interception is fail-closed if it is
// somehow absent.
func injectEgressCA(spec *sandbox.Spec, caDir string) error {
	certPath := tlsca.CertPath(caDir)
	if _, err := os.Stat(certPath); err != nil {
		return fmt.Errorf("egress CA cert not found at %s (broker not running, or interception unavailable): %w", certPath, err)
	}
	spec.Mounts = append(spec.Mounts, sandbox.Mount{
		Host: certPath, Container: egressCAPath, ReadOnly: true,
	})
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	for _, k := range []string{"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
		spec.Env[k] = egressCAPath
	}
	spec.Setup = append(spec.Setup,
		"cp "+egressCAPath+" /usr/local/share/ca-certificates/poddle-egress.crt 2>/dev/null && update-ca-certificates 2>/dev/null || true")
	return nil
}

// ensureHostAutoscaler starts the host-side reactive autoscaler for the daemon
// when a pod opts in (`up --autoscale`). A package var so tests can stub the
// spawn instead of launching a real detached process.
var ensureHostAutoscaler = poddled.EnsureHostAutoscaler

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
	Audit(e audit.Event) error
	SetPolicy(pod string, p *policy.Policy) error
	Egress(pod string) (token, addr string, err error)
}

// brokerNet puts the shared broker on a pod's internal lock network and resolves
// the broker's IP there — the pod's sole route out under egress lockdown.
// *podman.Provider satisfies it, so the engine doubles as this seam; tests pass
// a stub. It's an interface so buildSpec stays unit-testable without podman.
type brokerNet interface {
	EnsurePodLockNetwork(pod string) (string, error)
	ConnectBrokerToPod(brokerName, pod string) error
	BrokerIPOnPod(brokerName, pod string) (string, error)
}

// NewCmd builds the up command. --identity resolves an auth provider; --harness
// resolves a pod-side runtime. b talks to the persistent poddled broker.
func NewCmd(a *app.App, b podBroker) *cobra.Command {
	var image, size, identityName, harnessName, execCmd, templateName, policyName string
	var detach, autoscale bool

	c := &cobra.Command{
		Use:   "up [name]",
		Short: "Create a sandbox and connect to it",
		Example: `  # a shell wired to your work identity
  poddle up my-sandbox --identity work --harness claude-code
  # from a template, in the background
  poddle up api --template api --identity work --detach`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "poddle"
			if len(args) > 0 {
				name = args[0]
			}
			bn, ok := a.Engine.(brokerNet)
			if !ok {
				return fmt.Errorf("egress lockdown needs the podman engine")
			}
			spec, _, _, err := buildSpec(cmd, a, b, bn, buildOpts{
				name: name, image: image, size: size, identityName: identityName,
				harnessName: harnessName, templateName: templateName,
				allowSelect: !detach && execCmd == "" && a.Prompter != nil,
				withVolumes: true, // stateful session (workspace + agent state)
				autoscale:   autoscale,
				policyName:  policyName,
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
			_ = b.Audit(audit.Event{Pod: name, Kind: audit.KindPodUp, Detail: "size=" + spec.Size + " image=" + spec.Image, Decision: audit.DecisionAllow})

			// The autoscaler runs on the host now (the broker container has no
			// podman); spawn it, detached, when this pod opted in.
			if spec.Autoscale {
				ensureHostAutoscaler(poddled.SocketPath())
			}

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
	c.Flags().StringVar(&policyName, "policy", "", "governance policy to enforce on the pod's egress (from ~/.config/poddle/policies)")
	return c
}

// buildOpts carries the resolved flag values buildSpec needs.
type buildOpts struct {
	name, image, size, identityName, harnessName, templateName, policyName string
	allowSelect                                                            bool // interactively select an identity when none is given and a Prompter exists
	requireIdentity                                                        bool // error if no identity is resolved (an autonomous task needs auth)
	withVolumes                                                            bool // mount session state on named volumes (up/move; not ephemeral task pods)
	skipClone                                                              bool // don't clone the repo (move: the workspace volume already has it)
	autoscale                                                              bool // opt in to the daemon's reactive memory-grow autoscaler
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

// dirHasEntries reports whether path is a directory containing at least one entry.
func dirHasEntries(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

// hasVolumeContainer reports whether vols already mounts a volume at the given
// container path. Used to avoid registering a second, duplicate named volume when
// a harness's ConfigDir coincides with one of its StateDirs (e.g. codex, whose
// ConfigDir and StateDir are both /root/.codex) — podman rejects a duplicate
// mount destination.
func hasVolumeContainer(vols []sandbox.Volume, container string) bool {
	for _, v := range vols {
		if v.Container == container {
			return true
		}
	}
	return false
}

// buildSpec resolves the template + flag overrides, runs secret-safety, and
// issues broker handles (identity + connectors) against poddled — returning a
// ready-to-create spec and the resolved harness. It does not create the pod, so
// both `up` and `task` share it.
func buildSpec(cmd *cobra.Command, a *app.App, b podBroker, bn brokerNet, o buildOpts) (sandbox.Spec, harness.Harness, config.Template, error) {
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
		_ = b.Audit(audit.Event{Pod: o.name, Kind: audit.KindMountRefuse, Detail: err.Error(), Decision: audit.DecisionDeny})
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

	// Seed the user's custom harness config (settings/plugins/MCP declarations)
	// from ~/.config/poddle/harness/<harness>/ and persist the agent's config dir.
	// The seed mount is added after the mount security scan on purpose: it is a
	// poddle-owned config dir, not an arbitrary user mount, and the pod is
	// secretless regardless. Copy (not mount-in-place) so the agent keeps the dir
	// writable for its own session state.
	if cd := h.ConfigDir(); cd != "" {
		// Persist ConfigDir as a named volume — unless it's already registered as a
		// StateDir (codex's ConfigDir and StateDir are both /root/.codex); a second
		// identical volume would make podman reject a duplicate mount destination.
		if o.withVolumes && !hasVolumeContainer(spec.Volumes, cd) {
			spec.Volumes = append(spec.Volumes, sandbox.Volume{Name: stateVolName(o.name, cd), Container: cd})
		}
		if hostCfg := harnessconfig.Dir(harnessName); dirHasEntries(hostCfg) {
			const seedStage = "/run/poddle/harness-seed"
			spec.Mounts = append(spec.Mounts, sandbox.Mount{Host: hostCfg, Container: seedStage, ReadOnly: true})
			spec.Setup = append(spec.Setup, "mkdir -p "+cd+" && cp -a "+seedStage+"/. "+cd+"/ 2>/dev/null || true")
		}
	}

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

	// The broker is needed for any brokered credential (identity and/or
	// connectors) AND for a governance policy: a policy forces the pod's
	// arbitrary egress through the broker's forward proxy, which exists whether
	// or not the pod has a connector. Handles/policies live until `down`.
	policyName := flagOr(cmd, "policy", o.policyName, tpl.Policy)
	// A pod that names no policy inherits the configured default (if one is set
	// and still exists), so unattended pods are governed rather than running wide
	// open. An explicit `--policy ""` opts out and stays ungoverned.
	if policyName == "" && !cmd.Flags().Changed("policy") && a.Policies != nil {
		if ds, ok := a.Policies.(policy.DefaultStore); ok {
			if def, err := ds.Default(); err == nil && def != "" {
				if _, err := a.Policies.Get(def); err == nil {
					policyName = def
				}
			}
		}
	}
	if identityName != "" || len(tpl.Connectors) > 0 || (policyName != "" && a.Policies != nil) {
		if err := b.EnsureRunning(); err != nil {
			return fail(fmt.Errorf("start poddled: %w", err))
		}
		// Put the shared broker on this pod's internal lock network and learn its
		// IP there — that IP is the pod's ONLY route out. Fail-closed: any error
		// aborts the pod rather than leaving its egress unpinned.
		if _, err := bn.EnsurePodLockNetwork(o.name); err != nil {
			return fail(fmt.Errorf("lock network: %w", err))
		}
		if err := bn.ConnectBrokerToPod("poddle-broker", o.name); err != nil {
			return fail(fmt.Errorf("connect broker: %w", err))
		}
		brokerIP, err := bn.BrokerIPOnPod("poddle-broker", o.name)
		if err != nil {
			return fail(fmt.Errorf("broker peer ip: %w", err))
		}

		addr, err := b.Gateway()
		if err != nil {
			return fail(fmt.Errorf("broker gateway: %w", err))
		}
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return fail(fmt.Errorf("broker address %q: %w", addr, err))
		}
		// Every pod-facing address is pinned to the broker's peer IP on the lock
		// net; ports records each egress channel the pod actually uses so the
		// allow-list (below) covers exactly those and nothing else.
		ports := map[brokerendpoint.Channel]string{brokerendpoint.Gateway: port}
		podBrokerAddr := net.JoinHostPort(brokerIP, port)

		// egressHosts seeds a default-deny allow-list for a pod with no explicit
		// policy: exactly the hosts it needs (identity API, HTTP connectors, and
		// the harness's install/runtime hosts). Everything else is denied, so a
		// brokered pod is contained by default, not merely audited.
		var egressHosts []string
		if identityName != "" {
			apiHost, err := applyIdentity(b, a.Identities, a.Providers, h, identityName, "http://"+podBrokerAddr, &spec)
			if err != nil {
				return fail(err)
			}
			egressHosts = append(egressHosts, apiHost)
			egressHosts = append(egressHosts, h.EgressHosts()...)
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
			// OAuth material is loaded (below) only for the mcp/http cases — an L4
			// datastore connection never has an oauth.json and must never see one:
			// applyRedisDatastore/applyPostgresDatastore always pass nil to
			// connector.Credential, which builds the real DSN credential.
			switch def.Transport {
			case "l4-redis":
				if redisPodAddr, err = podL4Addr(redisPodAddr, brokerIP, b.RedisAddr); err != nil {
					return fail(err)
				}
				if err := applyRedisDatastore(b, conn, def, o.name, redisPodAddr, &spec); err != nil {
					return fail(err)
				}
				if _, p, err := net.SplitHostPort(redisPodAddr); err == nil {
					ports[brokerendpoint.Redis] = p
				}
			case "l4-postgres":
				if pgPodAddr, err = podL4Addr(pgPodAddr, brokerIP, b.PostgresAddr); err != nil {
					return fail(err)
				}
				if err := applyPostgresDatastore(b, conn, def, o.name, pgPodAddr, &spec); err != nil {
					return fail(err)
				}
				if _, p, err := net.SplitHostPort(pgPodAddr); err == nil {
					ports[brokerendpoint.Postgres] = p
				}
			case "mcp":
				oauth, err := loadConnectorOAuth(a.Connections, cn)
				if err != nil {
					return fail(err)
				}
				host, err := applyMCPConnector(b, h, conn, def, o.name, "http://"+podBrokerAddr, oauth, &spec)
				if err != nil {
					return fail(err)
				}
				if host != "" {
					egressHosts = append(egressHosts, host)
				}
			default:
				oauth, err := loadConnectorOAuth(a.Connections, cn)
				if err != nil {
					return fail(err)
				}
				if err := applyConnector(b, conn, def, o.name, podBrokerAddr, oauth, &spec); err != nil {
					return fail(err)
				}
				// HTTP connectors egress through the policy-checked gateway, so a
				// default-deny pod must be allowed to reach the connector's host.
				cb := conn.BaseURL
				if !strings.Contains(cb, "://") {
					cb = "https://" + cb
				}
				if u, err := url.Parse(cb); err == nil && u.Hostname() != "" {
					egressHosts = append(egressHosts, u.Hostname())
				}
			}
		}
		// A public repo clone (repo= with no git connector) egresses to its own
		// host through the forward proxy. When the pod is otherwise contained by a
		// derived default-deny policy, allow that host too so the clone isn't
		// blocked. Guarded on len(egressHosts) > 0 so a repo-only pod (nothing else
		// to contain) keeps open egress rather than being newly locked to just the
		// repo host — which would break its harness install.
		if len(egressHosts) > 0 && tpl.Repo != "" && !o.skipClone {
			if host := repoEgressHost(tpl.Repo); host != "" {
				egressHosts = append(egressHosts, host)
			}
		}
		// Bind the pod's governance policy so the daemon enforces it on every
		// request the pod makes through the broker. An explicit policy is the
		// user's full intent. Otherwise a derived default-deny policy contains the
		// pod to exactly the hosts it needs — so it can't exfiltrate to unrelated
		// hosts out of the box while its own API, connectors, and `npm i` work.
		// (defaultPolicyName is skipped as an "explicit" name so `move`, which
		// replays buildSpec from the pod's policy label, re-derives it.)
		switch {
		case policyName != "" && policyName != defaultPolicyName && a.Policies != nil:
			pol, err := a.Policies.Get(policyName)
			if err != nil {
				return fail(err)
			}
			spec.PolicyName = policyName // labelled so the dashboard's pod view shows it
			if err := b.SetPolicy(o.name, pol); err != nil {
				return fail(err)
			}
			// An intercepting policy — whether the legacy all-hosts bool or a
			// per-host intercept_hosts list — needs the pod to trust the egress CA
			// so the broker can terminate TLS on its HTTPS egress (opt-in MITM). The
			// CA is the broker's own (poddled.EgressCADir()), so the pod trusts
			// exactly what the broker signs with.
			if pol.Intercepts() {
				if err := injectEgressCA(&spec, poddled.EgressCADir()); err != nil {
					return fail(fmt.Errorf("intercept: %w", err))
				}
			}
		case len(egressHosts) > 0:
			derived := &policy.Policy{Name: defaultPolicyName, AllowUpstreams: dedupeHosts(egressHosts)}
			spec.PolicyName = defaultPolicyName
			if err := b.SetPolicy(o.name, derived); err != nil {
				return fail(err)
			}
		}
		// Route ALL of the pod's arbitrary (non-brokered) egress through the
		// broker's forward proxy. Egress lockdown makes the broker the sole exit,
		// so a locked pod reaches the internet only THROUGH the broker, where its
		// policy governs it — an explicit policy, or the derived default-deny
		// allow-list bound above. Unconditional because a locked pod on the
		// --internal net has no resolver/route of its own — without this it cannot
		// even install its agent harness (npm/pip get ENOTFOUND). The broker's own
		// peer IP is excluded (NO_PROXY) so brokered traffic reaches the gateway
		// directly.
		if token, addr, err := b.Egress(o.name); err == nil {
			if _, port, err := net.SplitHostPort(addr); err == nil {
				proxy := "http://" + token + ":x@" + net.JoinHostPort(brokerIP, port)
				if spec.Env == nil {
					spec.Env = map[string]string{}
				}
				for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
					spec.Env[k] = proxy
				}
				spec.Env["NO_PROXY"] = brokerIP
				spec.Env["no_proxy"] = brokerIP
				ports[brokerendpoint.Forward] = port
			}
		}
		// Pin the pod's egress allow-list to exactly the broker peer's channels.
		ep := brokerendpoint.NewPeer(brokerIP, ports)
		spec.Network = &sandbox.Network{AllowList: ep.AllowList()}
	}
	return spec, h, tpl, nil
}

// applyIdentity wires an identity into the pod secretlessly: re-authenticate on
// the client if stale (never a dead cred into the pod), take the real
// Credential, issue a pod-scoped handle for it at poddled, and fold ONLY the
// harness's broker-pointing env + install commands into the spec. The real
// secret never touches the pod.
// applyIdentity returns the identity's real API host (from the credential's
// BaseURL) so the caller can allow-list it — the gateway policy-checks the pod's
// requests against that host, so a default-deny pod must permit its own API.
func applyIdentity(b podBroker, store *idn.Store, reg idn.Registry, h harness.Harness, name, podBrokerURL string, spec *sandbox.Spec) (apiHost string, err error) {
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

	handle, err := b.IssueHandle(spec.Name, spec.Name, cred) // scope = pod name
	if err != nil {
		return "", err
	}

	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	for k, v := range h.Env(podBrokerURL, handle) {
		spec.Env[k] = v
	}
	spec.Setup = append(spec.Setup, h.Provisions()...)
	if u, perr := url.Parse(cred.BaseURL); perr == nil {
		apiHost = u.Hostname()
	}
	return apiHost, nil
}

// loadConnectorOAuth loads persisted OAuth material for connection cn, if any
// — see connector.Credential, where it takes priority over the connection's
// static token. Most connections have no oauth.json (ok=false, no error):
// they keep using the static token. Callers are exactly the mcp and default
// (HTTP) connector cases; L4 datastore connections never call this — see
// applyRedisDatastore/applyPostgresDatastore, which always pass nil.
func loadConnectorOAuth(store *connector.Store, cn string) (*connector.OAuthMaterial, error) {
	oauthMat, ok, err := store.LoadOAuth(cn)
	if err != nil {
		return nil, fmt.Errorf("connection %q oauth: %w", cn, err)
	}
	if !ok {
		return nil, nil
	}
	return &oauthMat, nil
}

// applyConnector issues a pod-scoped handle for a connection's service token at
// poddled and folds the connector's pod wiring (env + setup) into the spec.
// Connector setup (e.g. git url.insteadOf) is PREPENDED so it runs before the
// repo clone. The real token never enters the pod.
func applyConnector(b podBroker, conn connector.Connection, def connector.Definition, podName, podBrokerAddr string, oauth *connector.OAuthMaterial, spec *sandbox.Spec) error {
	cred, err := connector.Credential(conn, def, oauth)
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

// applyMCPConnector wires a brokered remote MCP server. It issues a pod-scoped
// handle for the MCP token, exposes it in an env var, and APPENDS the harness's
// MCP registration (MCPWiring) to Setup — which runs after the harness install
// because applyIdentity (Provisions) has already run when the connector loop
// executes. Per the spike, Credential.BaseURL must be the server ORIGIN (no
// path); the agent-facing url is the broker gateway root + the server's endpoint
// path. Returns the MCP host to add to the egress allow-list.
func applyMCPConnector(b podBroker, h harness.Harness, conn connector.Connection, def connector.Definition, podName, brokerGatewayURL string, oauth *connector.OAuthMaterial, spec *sandbox.Spec) (egressHost string, err error) {
	cred, err := connector.Credential(conn, def, oauth) // BaseURL = full endpoint here
	if err != nil {
		return "", err
	}
	base := cred.BaseURL
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("mcp connection %q url: %w", conn.Name, err)
	}
	endpointPath := u.Path                   // e.g. /mcp — rides the request path
	cred.BaseURL = u.Scheme + "://" + u.Host // origin only (spike-proven)
	handle, err := b.IssueHandle(podName, podName+"/"+conn.Name, cred)
	if err != nil {
		return "", err
	}
	envVar := mcpEnvVar(conn.Name)
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	spec.Env[envVar] = handle
	agentURL := strings.TrimRight(brokerGatewayURL, "/") + endpointPath
	spec.Setup = append(spec.Setup, h.MCPWiring(conn.Name, agentURL, envVar)...)
	return u.Hostname(), nil
}

// mcpEnvVar is the pod env var holding the handle for MCP connection name.
func mcpEnvVar(name string) string {
	return "PODDLE_MCP_" + strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, strings.ToUpper(name))
}

// applyRedisDatastore issues a handle for a Redis connection at poddled and
// points the pod at the broker's L4 Redis address with the handle as its
// password. The real DSN (user+password) stays in the broker. L4 datastores
// are never OAuth-capable, so this always passes nil to Credential — an
// oauth.json must never be able to hijack a datastore DSN.
func applyRedisDatastore(b podBroker, conn connector.Connection, def connector.Definition, podName, redisPodAddr string, spec *sandbox.Spec) error {
	cred, err := connector.Credential(conn, def, nil)
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
// the broker. L4 datastores are never OAuth-capable, so this always passes nil
// to Credential — an oauth.json must never be able to hijack a datastore DSN.
func applyPostgresDatastore(b podBroker, conn connector.Connection, def connector.Definition, podName, pgPodAddr string, spec *sandbox.Spec) error {
	cred, err := connector.Credential(conn, def, nil)
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
// rewrites its host to the broker's peer IP on the pod's lock network — how a
// locked pod reaches the broker.
func podL4Addr(cached, brokerIP string, fetch func() (string, error)) (string, error) {
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
	return net.JoinHostPort(brokerIP, port), nil
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
