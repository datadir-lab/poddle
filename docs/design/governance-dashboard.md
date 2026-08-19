# Governance dashboard - design (sub-projects 3+)

Extends `governance-audit.md`. The audit spine (record + query + stream) ships and
is e2e-green. This adds the **visual dashboard**, designed from the start to be
**reused by the enterprise cloud tier**: same UI, two backends.

## Reusability principle

The reusable assets are (1) a **stable, versioned audit API contract** and (2) a
**static UI bundle that reads its data source at runtime**. The UI never knows
whether it talks to a local daemon or the cloud collector.

```
                    ┌─ local:  `poddle dashboard`  → 127.0.0.1:7333
  dist/ (bundle) ───┤     serves bundle at /  +  proxies /v1/audit* → daemon (UDS)
   go:embed'd       └─ cloud:  served by the cloud app  →  /v1/audit* = collector (auth)
```

- **Shared contract**: `GET /v1/audit` (query: pod/kind/decision/since/limit +
  time-range + cursor), `GET /v1/audit/stream` (SSE), `GET /v1/audit/verify`
  (chain integrity). Local `poddle dashboard` implements it by proxying the
  daemon; the cloud collector implements it at scale with auth + multi-tenancy.
- **Runtime config**: the bundle reads `window.__PODDLE__ = { apiBase, auth,
  multiHost }`. Local: `{apiBase:'/v1', auth:none, multiHost:false}`. Cloud:
  `{apiBase:'https://…/v1', auth:token, multiHost:true}`.

## Forward-compat (so cloud is not a rewrite)

- **`Source` field on `audit.Event`**: empty locally (one host), a host-id in
  cloud (many daemons ship into one collector). Multi-host views need it; adding
  it now is a free column.
- **Versioned contract** (`/v1`) at the dashboard-facing layer.
- **Pluggable auth** in the UI, driven by runtime config (no-op local, bearer/
  session cloud).

## UI stack

`src/web/dashboard/`: Preact + Vite + TypeScript (tiny bundle, reuses the npm
toolchain already in the repo for the marketing site). Builds to a static
`dist/`. Features: live SSE feed, filters (pod/kind/decision/time), per-pod
drill-down, blocks/redactions highlighted, and a hash-chain **verify badge**.

## Bundle → binary

`go:embed` needs committed assets inside the Go package. `task dashboard-build`
builds the SPA to `src/cli/dashboard/dist/`, which is **committed** so
`go build ./src/cli` works offline (no Node needed to build poddle, important
for a self-hostable CLI). A CI check rebuilds and git-diffs to keep it fresh
(same pattern as the existing `web-docs` cli.json regeneration).

## Task breakdown (each: red→green→commit→push, discussed first)

- **Task 3 - forward-compat API.** Add `Source` to `audit.Event` + the SQLite
  column (unreleased DB, no migration). Add `GET /audit/verify` daemon endpoint
  + `Client.VerifyAudit`; `poddle daemon audit --verify` prints chain status.
  Unit-tested.
- **Task 4 - `poddle dashboard` server.** A Go command binding `127.0.0.1:<port>`
  that serves the embedded bundle at `/` and proxies `/v1/audit`,
  `/v1/audit/stream` (SSE passthrough), `/v1/audit/verify` to the daemon over the
  UDS. `--port`, `--open`. Unit-tested (serves the page + proxies a query);
  embeds a placeholder bundle until Task 5 lands the real one.
- **Task 5 - the Preact/Vite SPA.** The dashboard app in `src/web/dashboard`;
  `task dashboard-build` → committed `dist/`. e2e: `poddle dashboard` serves the
  page and the `/v1/audit/stream` feed delivers a live event.
