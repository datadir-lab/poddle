# Governance - the enforce half (policy + forced egress)

Extends `governance-audit.md`. The observe half (audit + dashboard) ships and is
e2e-green. This adds **governance**: policies decide what a pod may do, enforced
at the broker, every decision recorded into the audit spine.

Research basis (see `governance-dashboard.md` sources): default-deny egress via a
credential/forward proxy, **no MITM by default** (CONNECT + destination
allow-list), enforced below the agent. poddle already terminates the credentialed
upstreams (full content for free); a forward-proxy covers the rest by destination.

## Policies are first-class, named, referenced

`~/.config/poddle/policies/<name>.toml`: owner-scoped, reusable, auditable
objects (this maps cleanly to the cloud tier, where policies are governed
centrally). A pod references one: `policy = "prod-readonly"` in a template, or
`poddle up --policy prod-readonly`.

```toml
# ~/.config/poddle/policies/prod-readonly.toml
allow_upstreams = ["api.anthropic.com", ".internal"]  # default-deny when non-empty; ".x" = any subdomain
deny_upstreams  = ["metadata.google.internal"]        # always denied (wins)
methods = { forgejo = ["GET"], "api.anthropic.com" = ["POST"] }  # per host/connector
egress  = "block"   # redact (default) | block | off
```

```go
// internal/policy
type Policy struct {
    Name           string
    AllowUpstreams []string            `toml:"allow_upstreams"`
    DenyUpstreams  []string            `toml:"deny_upstreams"`
    Methods        map[string][]string `toml:"methods"`
    Egress         string              `toml:"egress"`
}
// Decide evaluates one request. Order: deny-list wins, then allow-list
// (default-deny when non-empty), then per-host method rules, else allow.
func (p *Policy) Decide(host, method string) (allow bool, reason string)

type Store struct{ dir string }
func (s *Store) Get(name string) (*Policy, error) // load <name>.toml
```

Host matching: exact, plus a `.suffix` entry matching any subdomain.

## Enforcement points (both are the one broker chokepoint)

- **Reverse-proxy (brokered upstreams), Task 2.** The gateway already gates
  *reachability* (a pod only reaches what it has handles for); policy adds finer
  control (e.g. read-only: `forgejo` GET-only) on that credentialed, highest-risk
  traffic. Zero new infra. Deny → 403 + `policy.deny` audit; allow → existing
  redact/proxy path.
- **Forward-proxy (arbitrary egress), Task 3.** The broker gains a CONNECT +
  HTTP forward-proxy; the pod is wired `HTTP_PROXY=broker`. Destination allow-list
  from policy; every destination allow/denied + audited (`policy.allow`/`deny`,
  `egress`). This is the market-gap capability: the agent can't reach an
  un-allow-listed host.
- **Network lockdown (non-bypassable), Task 4.** Pod on an internal network with
  the broker as sole exit, so `HTTP_PROXY` can't be bypassed. Infra-heavy in
  rootless nested podman (like resize/stats), feasibility-gated; the forward-proxy
  is testable without it.

## Binding a policy to a pod

The CLI resolves the named policy at `up`/`task` and sends the *resolved* policy
to the daemon (`POST /pods/{pod}/policy`); the daemon stores pod→policy and, in
its `broker.PolicyChecker` implementation, maps handle→pod→policy→Decide. The
gateway calls the checker before proxying. `down`/revoke clears it.

## Task breakdown (each: red→green→commit→push, discussed first)

- **Task 1 - policy model + evaluator + store** (`internal/policy`): the Policy
  type, TOML loading from the policies dir, and `Decide(host, method)`. Pure
  logic + file I/O, fully unit-tested. No wiring.
- **Task 2 - enforce on the reverse-proxy:** gateway `PolicyChecker` seam +
  deny→403; daemon checker (handle→pod→policy) + `POST /pods/{pod}/policy` +
  pod→policy store; `Client.SetPolicy`; template `policy` field + `--policy`
  flag; `policy.deny`/`policy.allow` audit. Unit + e2e (read-only policy denies a
  POST, allows a GET, deny is audited).
- **Task 3 - forward-proxy** for arbitrary egress + `HTTP_PROXY` wiring + audit.
  Unit + e2e (allowed host proxies, denied host 403s).
- **Task 4 - network lockdown** (non-bypassable). Feasibility-gated e2e.
