# Governable & auditable pods - design

## Goal

Every security-relevant thing an agent's pod does (which upstreams it reached,
what got redacted or blocked, when a handle was issued or revoked, when a pod was
created/moved/torn down) must be **audit-logged** and visible in a **dashboard**
(local now, cloud-hosted later). And it must be **governable**: policies decide
what a pod may do, enforced at the broker, with every allow/deny recorded. This
is poddle's Enterprise/governance tier (the original Vernir thesis) built on the
secretless broker that already exists.

## Why this is the right bet (research basis)

Egress control + audit is the **missing feature across the AI-sandbox market**
(E2B has no outbound filtering; Daytona has no network policy by default), and a
compromised agent with open egress can exfiltrate regardless of filesystem
isolation. The recommended pattern everywhere is a **credential-injection proxy +
default-deny egress allowlist** enforced below the agent, which is exactly what
poddle's broker already is.

On TLS inspection, the consensus is **do not MITM by default**: a CONNECT / SNI
domain-allowlist proxy preserves end-to-end encryption, avoids the CA-trust
burden, and survives Encrypted ClientHello (which is breaking selective MITM).
The closest analog (an engineer sandboxing coding agents) and the most mature
production system (StepSecurity Harden-Runner) both do domain-allowlist,
default-deny, **no MITM**, enforced at L3/L4 (not DNS-only; DNS-only has known
bypasses). Audit best practice: log every action with identity + destination +
policy outcome (never the secret), append-only + hash-chained (tamper-evident),
with separation of duties.

**poddle's unfair advantage:** it already *terminates* the credentialed upstreams
(LLM, connectors, DBs), so it gets **full content audit + redaction for free on
the highest-risk traffic**, with no MITM. A CONNECT-allowlist forward proxy covers
everything else at the destination level. One chokepoint, two modes; MITM becomes
a rarely-needed opt-in, not the foundation.

## Target architecture

- **Forced egress:** each governed pod runs on a network locked default-deny at
  L3/L4 (internal network / nftables), with the broker as the **sole exit**.
- **Two proxy modes at the one chokepoint (`poddled`):**
  - **Brokered upstreams** (LLM/connectors/DBs) → the existing reverse-proxy →
    **full content** (redaction, path/method policy).
  - **Everything else** → a new **CONNECT + domain-allowlist forward proxy** →
    **destination-level** allow/deny + audit, default-deny.
  - **(opt-in strict tier, later)** broker CA / MITM for full content everywhere.
- **Audit spine:** the daemon records every allow/deny/redact/issue/revoke/
  lifecycle event, hash-chained and secret-free, into a local store.
- **Dashboard:** `poddle dashboard` serves a local live view; a cloud collector
  reads the same event stream for the multi-host Enterprise tier.

## Decomposition (each is its own spec → plan → build)

1. **Audit spine + local dashboard**: *this doc.* The observe half and the
   foundation every enforcement decision records into.
2. **Forced egress**: CONNECT forward-proxy in the broker + pod network
   default-deny (L3/L4) + sole-exit routing. Emits egress events into the spine.
3. **Policy engine**: per-pod/identity/template rules (destination allow-lists,
   method/path, egress mode, later: approval gates) enforced at the
   gateway/proxy; every decision audited.
4. **Dashboard polish → cloud collector**: richer UI, then multi-host ingest,
   retention, SSO, WORM/tamper-evident storage (Enterprise).

---

# Sub-project 1: the audit spine + local dashboard

## Principles

- **The daemon is the single writer.** `poddled` already sees the rich runtime
  events; it owns the store, serialises appends, and maintains the hash chain.
  Client-side events (pod lifecycle, mount refusals) are POSTed to it.
- **Secret-free by construction.** No bodies, no resolved secrets, no URLs with
  query-strings. Log *that* a secret was redacted (rule + count), never the
  secret. This is enforced at the event-construction layer, not left to callers.
- **Tamper-evident.** Each row carries `prev_hash` + `hash` = SHA-256 over the
  canonical event + prev_hash. A verifier walks the chain; any edit/delete breaks
  it.

## Event model

```go
// internal/audit
type Event struct {
    Seq      int64     // monotonic, assigned by the store
    Time     time.Time // daemon clock
    Pod      string    // pod name ("" for daemon-level events)
    Identity string    // actor: owner or delegated driver ("" if n/a)
    Kind     Kind      // see below
    Upstream string    // destination host only (no scheme/path/query)
    Method   string    // HTTP method, when applicable
    Path     string    // request path WITHOUT query-string
    Status   int       // HTTP status or 0
    Decision Decision  // allow | redact | block | deny | ""
    Detail   string    // e.g. "redacted 1 secret (AKIA…rule)", "size=1.2MB" - NEVER a secret
    PrevHash string
    Hash     string
}

type Kind string // pod.up|pod.task|pod.move|pod.down | handle.issue|handle.revoke |
                 // request | egress | redact | block | l4.connect | mount.refuse |
                 // autoscale.grow|autoscale.warn | policy.allow|policy.deny
type Decision string // allow | redact | block | deny
```

## Store (SQLite, pure-Go modernc, no cgo)

```go
type Filter struct { Pod, Kind, Decision string; SinceSeq int64; Limit int }

type Store interface {
    Append(e Event) (Event, error)      // assigns Seq/Time/PrevHash/Hash, chains, persists
    Query(f Filter) ([]Event, error)    // newest-first, filtered
    Subscribe() (<-chan Event, func())  // live tail for SSE; func() unsubscribes
    Verify() (ok bool, brokenAt int64)  // walk the hash chain
    Close() error
}
```

- DB at `$XDG_STATE_HOME/poddle/audit.db` (fallback: config dir), one `events`
  table indexed on `(seq)`, `(pod)`, `(kind)`, `(decision)`.
- `Append` is mutex-serialised: reads the last `hash`, computes this row's
  `hash = sha256(canonical(e) + prev_hash)`, inserts, fans the event out to
  subscribers. Retention (max rows / max age) is a config knob (default: keep
  all locally; prune later).
- `modernc.org/sqlite` is pure Go (no cgo) so cross-compilation and the static
  binary are unaffected.

## Emit points (all existing chokepoints)

| Kind | Where | Detail (secret-free) |
|---|---|---|
| `request` | `gateway.ServeHTTP` | upstream host, method, path, status |
| `redact` / `block` | `gateway.redactBody` | rule/hit-count, size |
| `handle.issue` / `handle.revoke` | daemon `POST`/`DELETE /pods/{pod}` | scope |
| `l4.connect` | `l4.ServeRedis`/`ServePostgres` | upstream host |
| `autoscale.grow` / `autoscale.warn` | autoscaler `Log` | old→new size, mem% |
| `pod.up`/`task`/`move`/`down`, `mount.refuse` | CLI → `POST /audit` | size/image, blocked path |

The autoscaler already feeds a daemon event ring; that ring becomes a thin
adapter over `Store.Append`.

## Exposure (daemon control API, over the owner-only UDS)

- `POST /audit`: the CLI submits a client-side event (lifecycle, mount refusal).
- `GET /audit?pod=&kind=&decision=&since=&limit=`: filtered query (JSON array).
- `GET /audit/stream`: Server-Sent Events live tail.

## Dashboard

`poddle dashboard [--port]` binds `127.0.0.1:<port>` (local-only), reads the
audit store over the daemon's Unix socket, and serves a **single self-contained
HTML/JS page** (no external assets): a live SSE feed, filters (pod / kind /
decision / since), a per-pod drill-down, and blocks/redactions highlighted. It
also shows chain-verification status. The cloud collector (sub-project 4) later
consumes the same `GET /audit/stream`.

## Testing

- **Unit:** `Append` chains hashes and `Verify` catches a tampered/deleted row;
  `Query` filters by pod/kind/decision/since; `Subscribe` delivers live events;
  event construction strips query-strings and never stores a provided secret.
- **e2e:** a real `poddle up`/`task` through the broker produces `request` +
  `handle.issue` + `redact` events queryable via `poddle daemon audit` (a CLI
  wrapper over `GET /audit`); a run that trips egress redaction shows a `redact`
  event with the rule but not the secret. The dashboard page renders (served
  content asserts). Reuses the existing nested-podman e2e harness + AWS mirror.

## Task breakdown (each: red→green→commit→push, discussed first)

- **Task 1 - `internal/audit`:** `Event`/`Kind`/`Decision`, the SQLite `Store`
  (append+chain, query, subscribe, verify), the secret-free event constructor.
  Add `modernc.org/sqlite`. Unit-tested. No wiring yet.
- **Task 2 - daemon owns the store + emits + control API:** wire `Store` into
  `poddled`; emit at gateway/handles/l4/autoscale; add `POST /audit`,
  `GET /audit`, `GET /audit/stream`; CLI client method + emit lifecycle/mount
  events; `poddle daemon audit` to query. Unit + e2e.
- **Task 3 - `poddle dashboard`:** the local server + self-contained live UI
  over the SSE stream. Unit (handler) + e2e (page renders, feed streams).
