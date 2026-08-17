# Reactive memory-grow autoscaler (VPA) — design

## Goal

A pod running an autonomous agent can slowly grow its memory footprint and hit
its ceiling — at which point the kernel OOM-kills it. Memory is incompressible:
you cannot shrink a pod below its live usage, and you cannot hand a running
process more RAM in place (rootless cgroups won't even allow the live update).
The answer is to **move the session onto a bigger shell** — which, since the
resume-on-move work, transparently resumes the agent.

This feature makes that automatic: an opt-in background control loop in the
`poddled` daemon watches a pod's memory and, when it sustainedly nears its
ceiling, **grows** it one size tier. Grow-only, by design.

## Decisions

- **Opt-in, per pod.** A pod opts in at creation with `--autoscale` (or a
  template field), which sets a `poddle.autoscale=true` label. The daemon only
  ever touches labeled pods. Moving recreates the container, so it must be
  deliberate; default off.
- **Scope by mode.**
  - `headless` (task) pods → **auto-grow + auto-resume** in the background. A
    human isn't watching, and the daemon can't reattach a TTY, so headless is
    the natural fit and resume-on-move already handles it.
  - `interactive` (up) pods → **warn only**. The daemon can't reattach a human's
    terminal after recreating the container, so it never auto-moves them; it
    surfaces "pod X near its memory limit — run `poddle move X --size strong`".
  - `exec` / one-shot → ignored (nothing to scale).
- **Grow-only ladder:** `weak → strong`. At `strong` (top tier) a still-pressured
  pod is logged once and left alone (no tier above it yet).
- **Lives in the daemon.** `poddled` is already the always-on host process and
  already tracks active pods. "Auto-scaling without a command" means it happens
  in the background for opted-in pods — not a process the user babysits.

## Control loop

`src/internal/poddled/autoscale.go`, dependency-injected so it is fully
unit-testable without podman or a clock:

```
type PodStat struct { Name, Mode, Size string; MemPercent float64; Autoscale bool }
type StatsSource func() ([]PodStat, error)      // prod: podman ps/stats over labeled pods
type Mover       func(pod, size string) error   // prod: exec `poddle move <pod> --size <size>`
type WarnFunc    func(pod string, memPct float64)

type Autoscaler struct {
    Interval, Cooldown time.Duration  // 15s poll, 60s per-pod cooldown after acting
    HighWater          float64        // 85.0 (%)
    Sustain            int            // 3 consecutive ticks over HighWater before acting
    Stats  StatsSource
    Move   Mover
    Warn   WarnFunc
    Now    func() time.Time           // injectable clock
    // per-pod: consecutive-high count, cooldownUntil, warned latch
}
```

Each tick, for every opted-in pod:
1. `MemPercent >= HighWater` increments its streak; below resets it to 0.
2. When the streak reaches `Sustain` and the pod is not in cooldown:
   - `headless` and `Size != top` → `Move(pod, nextTier(Size))`; record a `grew`
     event; set `cooldownUntil = Now + Cooldown`; reset streak.
   - `headless` and `Size == top` → log "still pressured at max tier" once.
   - `interactive` → `Warn(pod, mem)` once; set cooldown (so it doesn't warn
     every tick); reset streak.
3. Hysteresis (`Sustain` ticks) rejects transient spikes; cooldown prevents
   thrashing right after a move while the new shell settles.

## Move must be context-free (foundation)

The daemon triggers a move with no working directory / `.poddle.toml`. So `move`
must reconstruct the pod from the **existing pod's labels**, not from cwd. Today
`poddle move X --size strong` in a bare directory silently reverts to the default
debian image and drops the identity — a latent bug. Fix:

- Label pods at create with everything a rebuild needs. Already present:
  `poddle.size`, `poddle.mode`, `poddle.repo`, `poddle.template`. **Add**
  `poddle.image`, `poddle.identity`, `poddle.harness`.
- `move` reads the source pod's labels as defaults (flags and an explicit
  `--template` still override), so `poddle move X --size strong` from anywhere
  preserves image + identity + harness + repo + mode.

## Observability

The daemon keeps a bounded ring of recent autoscale events (timestamp, pod,
action, mem%). `GET /status` includes them and `poddle daemon status` prints the
recent ones — this both delivers the interactive **warn** and makes every grow
auditable. Complements the already-shipped `poddle stats`.

## Testing

- **Unit (thorough):** the `Autoscaler` with a fake `StatsSource` (scripted mem%
  sequences), fake `Mover`/`Warn`, and an injected clock. Assert: grows only
  after `Sustain` sustained ticks; respects cooldown; grow-only; `weak→strong`;
  skips non-opted-in, non-headless-for-grow, and already-`strong`; warns
  interactive once per episode. Deterministic, no podman.
- **e2e (real, via a stats seam):** rootless nested podman has no cgroup, so
  real `podman stats` returns nothing in CI (the limit that skips stats/resize
  e2e). So the production stats source is **injectable**: when
  `PODDLE_AUTOSCALE_STATS=<file>` is set, the daemon reads synthetic pod mem%
  from that JSON file instead of `podman stats` (and `PODDLE_AUTOSCALE_INTERVAL`
  speeds the loop). An e2e then: `task --autoscale` a real headless pod, feed a
  stats file marking it 95%, and assert the daemon autonomously fires a real
  `poddle move` — a NEW container, grown to `strong`, with the agent resumed.
  This exercises the whole wiring (loop → mover → real move → recreate + resume)
  in CI; only podman's own stats-reading is out of scope. The label-inheritance
  foundation is already e2e-covered (Task 2: move with no `.poddle.toml`).

## Task breakdown

- **Task 1 — Autoscale opt-in:** `--autoscale` flag on `up`/`task` + template
  field → `Spec.Autoscale` → `poddle.autoscale=true` label. Unit-tested.
- **Task 2 — Move inherits pod spec from labels:** add image/identity/harness
  labels; `Engine.PodSpec(name)`; `move` defaults from labels. Unit + e2e
  (move with no `.poddle.toml`).
- **Task 3 — Autoscaler loop:** `poddled/autoscale.go` pure logic + full unit
  suite.
- **Task 4 — Daemon wiring + events:** production `StatsSource` + `Mover` +
  `WarnFunc` + event ring; start the loop in `Serve`; surface events in
  `poddle daemon status`.

Each task is its own red→green→commit→push, discussed before coding.
