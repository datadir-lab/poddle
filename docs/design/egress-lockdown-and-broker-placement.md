# Non-bypassable egress + broker placement

Extends `governance-enforce.md`. Tasks 1–3 (policy model, reverse-proxy enforce,
forward-proxy) ship and are e2e-green. This is **Task 4 — network lockdown
(non-bypassable)** — the deferred, feasibility-gated capstone — plus the
**broker-placement abstraction** that makes "the broker is the sole exit" hold
for *remote* pods, not only local ones.

## Why now

Today, "route through the broker" is **configuration, not enforcement**. The
broker's addresses reach the pod as env (`HTTP_PROXY`, `REDIS_HOST`, `PGHOST`)
and config files (git `insteadOf`, `.npmrc`). A cooperative agent honors them; a
**prompt-injected or malicious agent** can `unset HTTP_PROXY`, rewrite the
config, and egress directly — bypassing the forward proxy, the policy, and the
audit log. The governance promise is only as strong as the agent's goodwill.

Two properties close the gap:

1. **Non-bypassable egress.** The pod's network is default-deny with the broker's
   data channels as the *only* reachable destinations. Rewriting `HTTP_PROXY` is
   pointless — there is nowhere else for a packet to go. Policy and audit become
   unavoidable: a denied host is a broker `403`, never a silent direct connect.
2. **Untouchable control plane.** The pod can reach the broker's **data** plane
   and nothing else. It can never rebind its own policy, mint a handle, revoke,
   or read a credential — those verbs live only on the owner-only control socket,
   which is never in the pod's allow-list, never tunneled, never pod-facing.

Together: an agent can neither **escape** the broker nor **change** its
governance. This is the property the design must prove, adversarially, in e2e.

## Broker placement (first-class)

Everything is one sentence — *"the pod holds a handle and points at a broker"* —
so **broker placement becomes a resolver**, not a hardcoded address. `buildSpec`
stops computing `host.containers.internal:<localport>` and instead asks a
`BrokerEndpoint` for each channel's pod-facing address. One interface, three
strategies:

| Placement | Broker runs on | Pod reaches it via | Exposure | Best for |
|---|---|---|---|---|
| `colocated` | the pod's own host (as a dual-homed container) | its peer IP on the pod's internal net | none (internal bridge) | trusted host / your own always-on server — fire-and-forget |
| `direct` | a routable trusted host (VPS / poddle-cloud) | its address | **network → needs TLS** | managed always-on broker |
| `tunnel` | your laptop / NAT'd box | reverse SSH tunnel (`ssh -R`) | none (inside ssh + loopback) | attached "I'm working now" sessions |

```go
// internal/broker (or a small internal/brokerendpoint package)
type Channel int // Gateway, Forward, Redis, Postgres

// Endpoint resolves the pod-facing address for a channel under a placement, and
// the allow-list the egress lockdown must pin to. It has NO control-plane verbs.
type Endpoint interface {
    Addr(Channel) (string, error) // e.g. "host.containers.internal:16379"
    AllowList() []HostPort         // exactly the data channels — never control
}
```

The lockdown's allow-list is **derived from the same Endpoint**, so the pinhole
and the pod's config can never drift apart: the only address the pod is *told*
to use is the only address it *can* reach.

## The lockdown mechanism (the hard, feasibility-gated part)

Target: rootless podman (poddle's primary, and the CI e2e environment).

- The pod joins an **internal podman network** (netavark `internal: true`) — a
  bridge with **no masquerade/route to the internet**. The pod cannot reach any
  external host on any protocol.
- The **broker's data listeners are the sole reachable destinations** on that
  network — bound on the internal bridge's host-side gateway IP (`colocated`), or
  reached via the tunnel/routable pinhole (`tunnel`/`direct`). Nothing else on
  the network is routable.
- **DNS is an egress channel too.** On the internal network the pod gets **no
  external resolver**; name resolution for allowed destinations happens **at the
  broker** (the forward proxy already resolves `CONNECT host:port`; the gateway
  resolves the credentialed upstream). The pod resolves nothing on its own, so
  DNS-tunnel exfiltration is closed with the rest.
- The **control socket is explicitly excluded** from the allow-list and is never
  bound on the internal bridge.

**Fail-closed is mandatory.** If poddle cannot establish the internal network +
sole-exit binding (e.g. the podman/netavark primitive is unavailable on the
host), it **refuses to create the pod** with a clear error. It must never fall
back to an open-egress pod — a silent downgrade would defeat the whole property.

Feasibility risk (flagged in `governance-enforce.md`): reaching a host-side
broker from an `internal` network in rootless nested podman is the crux. The
implementation plan's first step is a spike that proves — in the CI e2e
environment — a pod on the internal network can reach the broker gateway and
**cannot** reach `1.1.1.1`. If the chosen primitive doesn't hold, alternatives
to evaluate: pod attached to the internal bridge with the broker as a peer;
pasta outbound filtering; nftables in the pod netns. The spike decides.

### Spike outcome (RESOLVED, 2026-08-20)

Two CI `workflow_dispatch` probes (`.github/workflows/spike-egress-lockdown.yml`)
settled the crux:

- **`--internal` + host-side broker → NOT viable.** internet is blocked ✅, but a
  pod on the `--internal` net **cannot** reach a broker listening on the host
  (neither `host.containers.internal` nor the bridge gateway routes to it). Run
  32420912388: `internet_blocked=yes broker_reachable=no`.
- **`--internal` + broker as a dual-homed container PEER → FEASIBLE.** Run
  (peer-v2): **`peer=yes isolated=yes relay=yes`**. The pod joins only
  `poddle-lock-<pod>` (`--internal`); the broker peer is attached to **two**
  networks — an internet-capable net as its **primary** (so its default route
  reaches upstreams) and `poddle-lock-<pod>` as a second interface (so the pod
  reaches it). Result: pod→broker works, pod→internet is cut off, broker→internet
  relays. All three legs green.

**Resolved mechanism:** the broker must sit **on the pod's internal network as a
dual-homed peer**, not on the host. Because `poddled` runs today as an
**in-process host daemon** (binds host listeners; pod reaches it via
`host.containers.internal`), realizing this needs a per-pod **dual-homed relay
container**: attached internet-primary + `poddle-lock-<pod>`-secondary, forwarding
the pod's four data channels to the host `poddled` (which it can still reach via
the host route). The pod talks only to the relay's internal-net IP; it has no
direct route anywhere else. This keeps `poddled` unchanged and is per-pod
disposable, torn down with the lock network. (Alternative — containerize
`poddled` itself as the dual-homed peer — is larger and defers to a later step.)

**Consequence for the foundation branch:** commit `26138d0` puts the *pod* on
`--internal` but leaves the broker host-side — the V1 (non-viable) shape. Its
argv unit tests pass, but a real brokered pod would be cut off from its broker.
`spec/broker-placement-egress-lockdown` must **not** merge until Task 4's network
realization is redirected to the relay-container mechanism above and Task 6's
adversarial e2e proves it end-to-end on CI.

## Realization (RESOLVED): containerized shared broker

The spike settles the mechanism; this is the authoritative realization for Step 1,
**superseding** the "colocated host-route" assumption in *The lockdown mechanism*
and *Component design* (poddled is no longer "unchanged for step 1").

**Topology — one shared broker container.** `poddle up` starts (once, then reuses)
a single long-lived `poddle-broker` container in place of today's auto-spawned
host subprocess (`poddle daemon`). It holds the one in-memory vault and is the
single writer of the hash-chained audit log. It is **dual-homed**:

- `poddle-egress` — a normal internet-capable podman network, attached
  **primary** so the broker's default route reaches upstreams (the spike's relay
  leg);
- `poddle-lock-<pod>` — each pod's `--internal` network, `podman network connect`ed
  on `up` and `disconnect`ed on `down`.

The **pod** joins only `poddle-lock-<pod>` (`--internal`): no default route, no
external DNS, no egress except to the broker peer on that net.

**Control plane stays host-only (untouchable by construction).** poddled keeps
serving its control API (mint / revoke / bind-policy / audit) over a **Unix
socket**, now on a host directory (`$XDG_RUNTIME_DIR/poddle/`) **bind-mounted**
into the broker container. The host CLI (`up`/`down`/`daemon`/`dashboard`) dials
that host path exactly as today. A pod gets **neither that mount nor any network
route** to the socket — so it cannot mint a handle, revoke, rebind policy, or read
the vault. The property holds because the control path is a filesystem socket the
pod was never handed, not because a rule forbids it.

**Secrets never touch the broker's disk.** The vault stays in-memory; the CLI
reads local token files on the host and feeds credentials over the control socket
(`POST /pods/{pod}/handles`), as today. Only the audit log and the control socket
are host-bind-mounted, so the audit timeline persists across broker restarts.

**Data plane.** The gateway, forward proxy, and L4 Redis/Postgres listeners bind
`0.0.0.0` inside the container; the pod reaches them at the broker's
`poddle-lock-<pod>` IP. `brokerendpoint` becomes a **peer resolver**: `Addr(ch)`
returns `<broker-internal-ip>:<port>` and `AllowList()` pins to exactly those.

**Packaging — published multi-arch image (cross-OS).** poddle is a single static
Go binary (`CGO_ENABLED=0`). The broker runs as **`poddle-broker`**, a minimal
image (distroless static base — bundles CA certs for the gateway's outbound TLS)
containing that binary, running `poddle daemon --socket /run/poddle/poddled.sock`.

*Why not mount the host binary:* on macOS/Windows hosts poddle drives a
**Linux-VM** podman; the host `poddle` binary is darwin/windows and cannot run in
a Linux broker container. A **published multi-arch (linux/amd64+arm64) image**,
pulled by the VM, is therefore required for cross-OS — not a later nicety. The
image tag tracks the CLI version, so a client always launches a matching broker.
`EnsureRunning` runs `ghcr.io/datadir-lab/poddle-broker:<version>`, overridable
via **`PODDLE_BROKER_IMAGE`** so e2e/CI point at a **locally built** image
(`podman build`) with no registry dependency.

*Feasibility sub-check (blocking, cheap):* confirm the static binary runs
`daemon` in a distroless container, its control socket is reachable over a host
bind mount, and the container dual-homes — before building Task 4 on it.

**Autoscaler needs podman.** poddled's opt-in autoscaler shells to `podman`
(`podman.New(exec.OS{},"")`). A containerized broker has no podman, so the host
podman socket is **bind-mounted** into the broker container (e.g.
`$XDG_RUNTIME_DIR/podman/podman.sock`) and poddled points at it. This is a host
*file* mount, never network-reachable by pods, so the control-plane property
holds; and the broker already holds every secret, so it adds no blast radius.

**Lifecycle / fail-closed.**

- `up`: ensure `poddle-egress` exists; ensure the `poddle-broker` container runs
  (start it dual-homed if absent); create `poddle-lock-<pod>` (`--internal`);
  connect the broker to it; resolve the broker's IP on that net; run the pod on
  it. **Any step failing → refuse to create the pod** (never an open-egress
  fallback).
- `down`: revoke handles (as today); disconnect the broker from
  `poddle-lock-<pod>`; remove that network. The shared broker container and
  `poddle-egress` persist for other pods.

**Revised Step-1 build order:** (0) static-binary-in-container feasibility
sub-check; (2) `brokerendpoint` **peer** resolver [supersedes `colocated`]; (3)
`Spec.Network` [done]; (4) podman provider — `poddle-egress` + broker-container
lifecycle + per-pod connect/disconnect + `--internal` pod, fail-closed [redirect];
(5) `buildSpec` wires the peer resolver's IP + allow-list [redirect]; (6)
adversarial e2e [unchanged].

## Per-placement egress + control separation

- **`colocated`** — pod internal network; broker binds the internal bridge
  gateway; control socket stays owner-only on the host, off the bridge.
- **`tunnel`** — only the 4 **data** `-R` forwards cross the tunnel; the control
  socket stays on the client, reached locally. poddled owns the `ssh -R`
  (system OpenSSH — inherits host-key verification, `~/.ssh/config`, agent), so a
  detached client keeps the tunnel up. Robustness: `ExitOnForwardFailure=yes`,
  `ServerAliveInterval`/`ServerAliveCountMax`, a supervision goroutine, teardown
  on `down`.
- **`direct`** — pod-facing routable address exposes **data channels only**, over
  **TLS** (handles gate auth; TLS gates eavesdropping); control is a separate,
  strongly-authenticated admin path (owner over SSH, or mTLS-admin later), never
  the address the pod knows. TLS on the channels is this mode's prerequisite.

## Config / UX

- `PODDLE_HOST` (existing) — where the pod's **compute** runs (`""` local,
  `ssh://…` remote).
- `PODDLE_BROKER` (new) — **placement**: `colocated` | `<addr>` | `tunnel`.
- Defaults: local compute + unset → `colocated`-local (today's behavior).
  **Remote compute requires an explicit `PODDLE_BROKER`** — the safe default
  (`tunnel`, secret stays home) and the useful default (`colocated`, always-on)
  genuinely conflict, so poddle refuses to guess.

## Component design

- `internal/broker` (or `internal/brokerendpoint`): the `Endpoint` interface +
  `colocated` resolver (step 1). Pure, unit-tested.
- `internal/sandbox` + `internal/podman`: `Spec` gains a `Network` (internal +
  allow-list); the podman provider realizes it (network create/attach, fail-closed
  if unavailable). Arg-building unit-tested against the `exec.Runner` fake, exactly
  like the existing provider methods.
- `cli/up` `buildSpec`: ask the `Endpoint` for each channel's address (replacing
  the hardcoded `podBrokerHost():port`) and pass the derived allow-list into the
  `Spec`. Keeps the existing `apply*Datastore`/`applyIdentity`/`applyConnector`
  shape; only the *source* of the address changes.
- poddled: **containerized** for step 1 — see *Realization (RESOLVED)* above. The
  daemon code is unchanged; only its *launch* moves from a host subprocess to a
  dual-homed `podman run`, with the control socket + audit log on host bind
  mounts. The tunnel/direct owners land in later steps.

## Build order (decomposition)

1. **Step 1 — non-bypassable lockdown (colocated-local) + `Endpoint` resolver +
   adversarial e2e.** The security core; independently valuable (hardens today's
   local pods). *This spec's implementation plan.* Gated by the feasibility spike.
2. **`colocated`-remote** — start/adopt poddled on a trusted compute host over
   SSH; pods there reach it locally. Ships fire-and-forget; no TLS, no tunnel.
3. **`tunnel`** — the reverse-SSH-tunnel owner in poddled (laptop/NAT).
4. **`direct` + TLS** — routable broker with TLS on the data channels.

## Error handling

- Lockdown primitive unavailable → **fail closed**, refuse to create the pod,
  name the missing capability.
- (`tunnel`) `ssh` missing / forward rejected / host-key mismatch → surfaced on
  `up`; never a silent open-egress fallback.
- Rootful podman (`host.containers.internal` ≠ loopback) → out of scope for step
  1; documented; the lockdown targets rootless.

## Testing

**Adversarial e2e is the deliverable — it proves the property, not the path.** A
pod is brought up wired to the broker with a policy, then, from *inside* the pod:

1. `curl https://1.1.1.1` (raw public IP, no proxy) → **fails at the network**.
2. `unset HTTP_PROXY HTTPS_PROXY && curl https://example.com` → **fails**.
3. `REDIS_HOST`/`PGHOST` pointed at a *different* real server → **unreachable**.
4. reach the **control socket/port**, or `POST` a policy / issue a handle / revoke
   against any reachable address → **fails** (no route; no such verb on the data
   plane).
5. Through the broker: an allow-listed host **succeeds**; a denied host returns
   the broker **`403`** (proving traffic went *through* the broker); the audit log
   **recorded** the attempts.

If any of 1–4 succeeds, the test fails loudly.

Unit: the `Endpoint` resolver (addresses + allow-list), the podman `Spec.Network`
arg-building (fake `exec.Runner`), and fail-closed when the primitive is absent.

## Open questions / risks

- ~~**Feasibility spike (blocking):** internal-network → host-broker reachability
  in rootless nested podman on the CI runner.~~ **RESOLVED** — see "Spike outcome"
  above. Host-side broker is unreachable from `--internal`; a dual-homed broker
  peer (relay container) is the proven mechanism.
- Rootful support (later).
- Whether `direct`-mode TLS reuses the connectors' cert story or a new one (step 4).
