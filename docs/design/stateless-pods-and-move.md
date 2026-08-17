# Stateless pods + `poddle move`

**Status:** design (2026-08-17). Reshapes the tail of Phase 2 (dynamic sizing →
session mobility).

## Motivation

Growing a pod's RAM in place is the wrong problem: memory is incompressible,
grow-only, OOM-risky, and needs cgroup delegation the runtime often can't give.
The right primitive is to **move the session to a right-sized pod** — you never
resize memory, you re-home. One primitive covers several needs:

- **needs more RAM / CPU** → move to a bigger shell
- **remote** (Phase 2's last item) → move to a shell on another host
- **pod died / OOM'd / poisoned** → move to a fresh shell (recovery)
- **upgrade base image** → move onto a new shell

It completes the secretless thesis: **secretless *and* stateless pods.** Secrets
live in the broker (injected as handles); state lives on volumes (mounted). The
pod holds neither — a disposable, sized compute *shell*. Throw it away, recreate
it anywhere, any size, instantly.

## Model

**Name-keyed volumes.** A pod named `proj` owns volumes named `poddle-proj-*`:

- `poddle-proj-workspace` → `/workspace` (code, working tree, deps, artifacts)
- `poddle-proj-<harness-state>` → each harness state dir (claude-code:
  `/root/.claude`, its conversation history)

Lifecycle:

- `poddle up proj` — create the volumes (once) + a shell bound to them. The repo
  clone lands in the workspace volume (persists).
- `poddle move proj [--size|--image|--host]` — remove the shell **keeping the
  volumes**, create a new shell with the new params + the same volumes,
  re-broker (fresh handle). Same-host: instant (volume reused). Cross-host:
  copies the volume (deferred).
- `poddle down proj` — remove the shell **and** its `poddle-proj-*` volumes
  (full teardown; the session is gone).

The pod name *is* the session identity; no separate `session` noun. `move`
preserves state, `down` destroys it.

## What persists

- `/workspace` — always.
- Harness state — the harness declares it. New interface method
  `StateDirs() []string` (claude-code → `["/root/.claude"]`). Each becomes a
  named volume mounted at that path. `~/.claude.json` (onboarding/config) is
  regenerated, not persisted.

Nothing else. Running processes are **not** migrated in the MVP — they restart
in the new shell (fine for agent work: you re-run a build, you don't checkpoint
it). CRIU checkpoint/restore for live process (and cross-host) migration is a
later "advanced" mode.

## `poddle move` flow

`move proj --size strong` ≈ `up proj` with three differences: reuse the existing
volumes, skip the repo clone (the workspace volume already has it), and remove
the old shell first.

1. Resolve the template/flags for `proj` (same path as `up`), new size/image.
2. Remove the old shell (`Engine.Remove`) — **do not** remove volumes.
3. Create the new shell with the `poddle-proj-*` volumes mounted, no clone.
4. Broker: issue fresh handles for the new shell (poddled), wire the env.
5. Attach (interactive) or return (detached). Old handles died with the old shell.

Cross-host move (`--host ssh://other`): the volume must be transferred
(`podman volume export | ssh … volume import`) — a copy, not instant. Deferred
to a follow-up; the same-host path (the RAM/CPU use case) is the MVP.

## Changes to dynamic sizing

`move` is the answer to "needs more resources," so the risky in-place bits go:

- Remove the `after_task` **memory** shrink — hooks resize **CPU only** (burst up
  for the run, drop CPU after). Memory is set right at shell creation / by move.
- `poddle resize` stays for **CPU** (burstable, safe). Memory resize is
  grow-only where cgroup delegation exists; the primary story for memory is
  `move`, not `resize`.

## Task plan

**Stateless pods (foundational — stands alone; also fixes "lose your work when a
pod dies"):**

- **V1** `sandbox.Spec` gains `Volumes []Volume{Name, Container}`; podman
  `Create` adds `--volume <name>:<path>` (named volumes auto-create). Unit/fake.
- **V2** `Harness.StateDirs()` (+ claude-code impl + fake). Unit.
- **V3** `up` derives `poddle-<name>-*` volume names, mounts `/workspace` + the
  harness state dirs; the clone targets the workspace volume. Unit.
- **V4** `down` removes the pod's `poddle-<name>-*` volumes
  (`Engine.RemoveVolumes` / list+remove). Unit + e2e (data survives a down only
  if we intend it to; here down purges).

**`poddle move`:**

- **M1** `poddle move <name> [--size|--image]` — remove shell (keep volumes),
  recreate with new params + same volumes, re-broker, no clone. Unit.
- **M2** e2e — `up`, write a file to `/workspace`, `move --size`, assert the file
  survives in a **different** container; `down` purges.

**Cleanup:**

- **C1** hooks/`resize` become CPU-only (drop the memory-shrink footgun).

Cross-host move + CRIU + reactive memory grow-on-pressure are explicit
follow-ups, out of this spec.
