# Broker isolation and blast radius (design decision + roadmap)

**Status:** the shared single-broker model is a deliberate MVP choice and ships
today. Per-tenant isolation is a poddle-cloud roadmap item (below); per-pod
isolation is deliberately *not* planned. This document records the decision so it
is not mistaken for an oversight.

Extends [`security-design.md`](../security-design.md) and
[`egress-lockdown-and-broker-placement.md`](./egress-lockdown-and-broker-placement.md).

## The current model

One **shared `poddle-broker` container per host** is the singleton: a single
vault holding every pod's credentials, a single audit writer, one control socket.
It is started on first `up`, restarted if stopped, and reused by every pod on
that host (see [`architecture.md`](../architecture.md#packaging--lifecycle)).

That means one process is the concentration point for the whole host:

- **One vault, all secrets.** Every pod's real credentials live in one process's
  memory. A full compromise of that process exposes every pod's secrets, not just
  the attacker's own pod.
- **One audit chain.** A single hash-chained log records all pods' activity. A
  compromised writer can withhold new events, and — with direct DB access — could
  truncate the tail or re-chain the whole log: the local chain is unkeyed, so it
  is tamper-evident against corruption and interior edits/deletes, not against a
  writer that rewrites it. An external anchor of the head (a co-signing witness /
  WORM sink, an Enterprise capability) is what makes writer tampering detectable
  across trust domains.
- **One point of failure.** If the broker crashes or wedges, every pod on the
  host loses brokered egress at once (fail-closed: they lose access, they do not
  fall through to direct egress).

This is the classic vault trade-off: concentrating secrets makes them *easier to
guard well* (one hardened surface, one audit chain, one thing to patch) at the
cost of a *larger blast radius if that one surface falls*.

## Why single-broker is the right MVP choice

- **One surface to harden, audit, and reason about.** A single vault process is
  where the [privilege-separation work](./broker-privilege-separation.md) and the
  container lockdown pay off; N brokers would multiply the surface to secure.
- **Single audit chain.** One tamper-evident log across the host is simpler to
  verify and stream than reconciling N per-pod chains.
- **Operationally trivial.** One container to start, restart, version-pin, and
  publish — matching poddle's "fire-and-forget on your own host" posture.
- **The host is already the trust boundary.** In the OSS single-host model every
  pod belongs to the *same* user on the *same* machine. Isolating one of that
  user's pods from another's credentials has limited value: they share the host,
  the podman socket owner, and the home directory the broker protects. The
  meaningful boundary is pod↔host, and that is enforced (secretless pods,
  owner-only control plane, egress lockdown) regardless of broker count.

## What already bounds the blast radius

The shared broker is not an unguarded single point — several properties limit
what a compromise or failure reaches:

- **Secretless pods.** No pod ever holds a real credential; it holds only opaque,
  revocable handles. Compromising a *pod* yields nothing from the vault.
- **Owner-only control plane.** Minting/revoking handles, rebinding policy, and
  reading credentials live only on the host-only control socket — never
  pod-facing. A pod cannot reach the verbs that would widen its own access.
- **Least-privilege, privsep-bound custody.** Tier 0/1 hardening (fuzzed parsers,
  `--cap-drop=all`, `no-new-privileges`, read-only rootfs) and the planned Tier 2
  custody/parsing split shrink the odds that parsing untrusted bytes escalates to
  reading the vault.
- **Revocation and fail-closed.** `poddle down` revokes a pod's handles
  immediately; a broker failure denies egress rather than leaking it.

The residual risk is specifically: *a full code-execution compromise of the vault
process reads all co-located pods' secrets at once.* That is what per-broker
isolation would reduce, and what the ladder below addresses.

## The isolation ladder

| Level | Vault scope | Blast radius of a vault compromise | Cost | Where |
|---|---|---|---|---|
| **shared (today)** | one broker / host | all pods on the host | lowest | OSS single-host |
| **per-tenant** | one broker / tenant | that tenant's pods | moderate (N vaults, N audit chains, a router) | **poddle-cloud** |
| **per-pod** | one broker / pod | that single pod | high (per-pod lifecycle, resource multiplication) | not planned |

**Per-tenant** is the meaningful next step, and it only becomes meaningful once
there *is* more than one tenant — i.e. the multi-tenant control plane, which is
poddle-cloud's domain, not the OSS single-host tool's. In
poddle-cloud, tenant boundaries are real trust boundaries (different customers,
BYO-infra, EU-first governance), so a compromise must not cross them; a
per-tenant vault (or a stronger KMS/HSM-backed custody model) is the right unit
there.

**Per-pod** brokers push isolation to the extreme but multiply every cost —
container lifecycle, memory, audit reconciliation, and the very hardening surface
we want to keep singular — for a boundary (one of your own pods vs. another) that
is rarely a real trust boundary. Not planned.

## Recommendation

- **OSS / single-host:** keep the shared single-broker model. Document the
  trade-off (this file) rather than build multi-broker into the single-host path.
- **poddle-cloud:** treat **per-tenant broker isolation** (and KMS/HSM-backed
  custody) as a roadmap item for the multi-tenant control plane, where tenant
  boundaries are genuine trust boundaries. Capture it in poddle-cloud's strategy
  docs so it is designed in, not retrofitted.
- Continue to shrink the *probability* of a vault compromise via the
  [privilege-separation ladder](./broker-privilege-separation.md); isolation
  bounds the *consequence*, hardening lowers the *likelihood*, and both matter.
