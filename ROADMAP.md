# poddle - Roadmap

**Vision:** self-hostable, secret-safe dev sandboxes for coding agents. Spin up
an isolated, reproducible pod on your own infra, wired to your self-hosted stack,
with your coding agent authed and no vendor secret ever inside the pod.

**Status (2026-08-16):** Phase 0 shipped. Identities MVP built but currently
injects a secret; Phase 1 replaces that with the secretless broker.

## Phase 0 - Local MVP (done)

- CLI: `up` (create + attach), `ls`, `down`.
- `engine.Engine` (podman, local + `PODDLE_HOST` remote over ssh).
- Vertical slices (`src/cli/*`) + shared kernel (`src/internal/*`).
- 4-tier tests (unit, architecture, integration, e2e), CI green on Woodpecker.

## Phase 1 - Harness + secretless broker (local, single-user)

- **Harness** (pod-side runtime): `claude-code`. Points the harness at the broker
  (base-url + handle).
- **Provider** (client-side auth): `anthropic`.
- **Broker** (local): holds the cred in memory, issues revocable handles, runs an
  injecting gateway that swaps the handle for the real auth on the wire. The pod
  holds a handle, never the secret.
- `up --harness --identity` installs the harness + broker-backed secretless auth;
  `down` revokes.
- Spec: `docs/design/secretless-identity.md`.

## Phase 2 - poddled (persistent) + reattach + assigned identity

- `poddled` service on the pod host; the broker runs in poddled (persistent,
  outlives the client).
- **Assigned identity**: pod-lifetime creds. Spin up, close the client, the agent
  keeps working, reattach later.
- Remote pods + reverse-tunnel egress to the broker (ssh-agent-forwarding model).
- **Dynamic vertical sizing** (cgroup live update, no restart): `size` is a CPU
  ceiling, so idle pods float to ~0 and burst up for free. `poddle resize` does a
  deterministic live resize; task hooks (`before_task` / `after_task`) let a
  workload scale itself with no detection lag. Opt-in reactive VPA auto-resizes
  CPU and grows memory on pressure (memory is grow-only). Idle scale-down and
  suspend for local pods are not shipped yet.

## Phase 3 - Collaboration

- `share` / `unshare` / `attach` / `evict` / `detach`; exclusive (evict-to-take)
  vs shared (coexist) modes.
- Owner base identity for autonomous runs; each active driver uses their own
  delegated identity, giving per-user creds, billing, ToS-clean access, and
  per-user attribution/audit.

## Phase 4 - Cloud + enterprise

- Multi-tenant broker with process-level tenant walls; on-prem and cloud
  deployments.
- Cloud UI (pods, identities, audit, team) + optional desktop app.
- Governance/compliance (DORA / AI-Act), SSO/SCIM, support/SLA; optional managed
  pods.
- Usage-based vertical autoscaling for managed pods: bill by actual CPU/mem used,
  with VPA right-sizing. The premium differentiator vs flat-size sandboxes.

## Cross-cutting

- Templates (env blueprints) + a harness registry (`claude-code`, `codex`,
  `aider`, `pi`, `local`).
- MITM-egress fallback for harnesses that don't honor base-url.
- Alternative pod runtimes (not shipped): the `sandbox.Runtime` label stubs
  `container-desktop` (GUI/VNC), `microvm`, and `vm`, but only `container` is
  wired today. Ship a full-desktop runtime and stronger isolation tiers.

> **Marketing vs code gaps to keep honest** (home-page audit, 2026-08): (1) full
> desktop runtime, see Cross-cutting; (2) hand a live pod to a teammate with
> per-driver identity, see Phase 3; (3) suspend when idle on local pods, see
> Phase 2. The home page describes only what ships today.

## Business model (open-core)

The core is free and open source (AGPL-3.0). The commercial editions - the hosted
control plane and enterprise governance - are proprietary and live in a separate,
private repository; see [LICENSING.md](./LICENSING.md) for what is licensed how.

Pricing and go-to-market are maintained privately and are not part of this repo.

## Design docs

- `docs/design/secretless-identity.md`: secretless identities + broker.
- (in ai-infra) `2026-08-15-poddle-design.md`, `TESTING.md`.
