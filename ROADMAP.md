# poddle — Roadmap

**Vision:** self-hostable, secret-safe dev sandboxes for coding agents. Spin up
an isolated, reproducible pod on your own infra, wired to your self-hosted stack,
with your coding agent authed — and **no vendor secret ever inside the pod**.

**Status (2026-08-16):** Phase 0 shipped. Identities MVP built but currently
injects a secret; Phase 1 replaces that with the secretless broker.

---

## Phase 0 — Local MVP ✅ done

- CLI: `up` (create + attach), `ls`, `down`.
- `engine.Engine` (podman, local + `PODDLE_HOST` remote over ssh).
- Vertical slices (`src/cli/*`) + shared kernel (`src/internal/*`).
- 4-tier tests (unit · architecture · integration · e2e), CI green on Woodpecker.

## Phase 1 — Harness + secretless broker (local, single-user)

The credential model, done right, from day one.

- **Harness** (runtime, pod-side): `claude-code` — `Provisions()` + `Materialize()` → points the harness at the broker (base-url + handle).
- **Provider** (auth, client-side): `anthropic` — `Authenticate` / `IsAuthenticated` / `Credential`.
- **Broker** (local): holds the cred in memory, issues **revocable handles**, runs an **injecting gateway** (swaps handle → real auth on the wire). The pod holds a handle, never the secret.
- `up --harness --identity` → install harness + broker-backed secretless auth; `down` → revoke.
- Spec: `docs/design/secretless-identity.md`.

## Phase 2 — poddled (persistent) + reattach + assigned identity

- `poddled` service on the pod host; the **broker runs in poddled** (persistent, outlives the client).
- **Assigned identity** = pod-lifetime creds → spin up, close the client, agent keeps working, **reattach later**.
- Remote pods + reverse-tunnel egress to the broker (ssh-agent-forwarding model).
- **Dynamic vertical sizing** — right-size a *running* pod, no restart (cgroup live update):
  - **Burstable by default**: `size` = a CPU *ceiling*; CPU is work-conserving, so idle pods float to ~0 and busy pods burst to the ceiling for free — no monitoring needed. Memory gets a generous safety cap.
  - **`poddle resize <pod> <size>`** — deterministic live resize (`podman/docker update`); the workload scales *itself* via **task hooks** (`before_task: resize strong` / `after_task: resize weak`) or an agent-callable command. Fits bursty work (e.g. a `docker compose` test) with **no detection lag**.
  - **Reactive VPA in poddled** (opt-in): watch cgroup stats, auto-resize CPU within `min`/`max`, and **grow** memory on pressure. Honest caveats — reactive scaling lags bursts (prefer hooks for those), and memory can't safely shrink below live usage (grow-only).

## Phase 3 — Collaboration

- `share` / `unshare` / `attach` / `evict` / `detach`; **exclusive** (evict-to-take-over) vs **shared** (coexist, no evict) modes.
- **Owner base identity** for autonomous runs; **each active driver uses their own delegated identity** → per-user creds, billing, ToS-clean, per-user attribution/audit ("bring your own login").

## Phase 4 — Cloud + enterprise

- **Multi-tenant broker** with process-level tenant walls; `on-prem` and `cloud` deployments (single- vs multi-tenant).
- **Cloud UI** (pods · identities · audit · team) + optional **desktop app**.
- Governance/compliance (DORA / AI-Act), SSO/SCIM, support/SLA; optional **managed pods**.
- **Usage-based vertical autoscaling** for managed pods: bill by *actual* CPU/mem used (not allocated), with VPA right-sizing — the premium cost differentiator vs flat-size sandboxes (this is where dynamic sizing turns into real $ savings, not just host-contention relief).

## Cross-cutting

- **Templates** (env blueprints) + a **harness registry** (`claude-code`, `codex`, `aider`, `pi`, `local`).
- **MITM-egress** fallback for harnesses that don't honor base-url.

---

## Business model (open-core)

Monetize the **control plane / governance per seat**; keep **compute BYO** (customer's
infra, $0) — don't fight the E2B/Daytona compute-margin war.

| Tier | Price (est.) | For | Compute |
|---|---|---|---|
| **OSS** | $0 | solo self-host, single-user | BYO |
| **Pro** | ~$12–19/mo | solo convenience (hosted broker + UI) | BYO / small managed bundle |
| **Team** | ~$25–40/user/mo | teams, SSO, collaboration, attribution, UI | BYO; managed optional |
| **Enterprise** | ~$30k–150k+/yr | on-prem/air-gapped, governance, compliance, support | BYO (their infra) |

- **Solos = funnel, not revenue** (they self-host free). **Orgs = the money**, and the
  secretless + per-user-ToS-billing + sovereignty story lets you price *above* a plain
  sandbox tool — you're selling *compliant, attributable agent access*.
- **Managed pods** (optional): thin markup (~$0.10–0.40/hr) + auto-suspend; convenience, not profit.
- Numbers are hypotheses — validate with 2–3 paying design-partner teams.

## Design docs

- `docs/design/secretless-identity.md` — secretless identities + broker.
- (in ai-infra) `2026-08-15-poddle-design.md`, `TESTING.md`.
