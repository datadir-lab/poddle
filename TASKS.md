# poddle — Atomic tasks

Work **one at a time, top to bottom** — not in parallel. Each task is a small
TDD unit: write the test, make it pass, `task ci` green, commit. Check it off.

---

## Phase 1 — Harness + secretless broker (local, single-user)

### Broker

- [x] **1.1** `internal/broker`: define `Credential{Mode,Vendor,Secret,BaseURL}` and `Handle{Value,Tenant,CredID,Scope}` types. Test: construct + zero-values.
- [x] **1.2** broker: in-memory `Vault` — `Store(tenant, cred) → credID`, `Get(tenant, credID)`, `Delete`. Test: store/get; get across a different tenant is denied.
- [x] **1.3** broker: `IssueHandle(tenant, credID, scope) → Handle` (random, high-entropy) + `Resolve(handleValue) → (Credential, ok)` + `Revoke(handleValue)`. Test: resolve returns the cred; revoked handle no longer resolves; cross-tenant resolve denied.
- [x] **1.4** broker: injecting HTTP handler — reads the handle from the incoming `Authorization`, resolves it, rewrites auth to the real secret per mode, reverse-proxies to the credential's upstream base URL. (`gateway.go`; tests in 1.H3.)
- [x] **1.5** broker: `Server` — `Serve(addr) (bound, error)` (binds sync, serves async, returns concrete bound addr) + `Stop(ctx)` (graceful `http.Server.Shutdown`). Test: serve on `127.0.0.1:0`, request round-trips to a fake upstream, stop → address refuses. Interface/container-reachability deferred to 1.12/1.14.

### Hardening (before wiring `up`)

- [x] **1.H1** broker: handle TTL — `Handle.ExpiresAt`, `IssueHandle(…, ttl)` (`ttl<=0`→`DefaultHandleTTL`=12h), injectable clock, `Resolve` returns new `ErrExpired` and lazy-deletes expired records. Tests: resolves before expiry; `ErrExpired` after; lazy delete; default TTL.
- [x] **1.H2** broker: vault memory hardening — secrets sealed in `memguard` enclaves (page-locked + encrypted-at-rest-in-memory); `Store`/`Get`/`Delete` signatures unchanged; `Get` copies out a transient plaintext then destroys the LockedBuffer; package `Purge()` wraps `memguard.Purge()` for shutdown (wired in root at 1.13). Note: per-request injection into a `net/http` header is unavoidably a short-lived plaintext string — hardening protects the long-lived vault copy, the high-value target.
- [x] **1.H3** broker: gateway tests (completes 1.4) — httptest upstream asserts per-mode header injection (`x-api-key` vs `Bearer`), handle stripped, method+path+query+body preserved, invalid/revoked/expired → 401. Race-clean.
- [x] **1.H4** broker: full round-trip test — real Vault+Handles+Gateway in front of httptest vendors. Modes + revoked/expired→401 are covered by 1.H3 (same full stack); 1.H4 adds the rest: **SSE streaming pass-through** (proven via a release-gated upstream — first event arrives before the second is sent) and **multi-tenant isolation** (each handle reaches only its own upstream + secret). In-package httptest, race-clean.

### Provider (auth) — refactor to hand a Credential, not a secret env

- [x] **1.6 + 1.7 (merged)** `identity.Provider`: **add** `Credential(id) → broker.Credential` to the interface and implement on `FakeProvider` and `identity/anthropic` (stored token → `Credential{Mode:subscription, Vendor:anthropic, Secret:token, BaseURL:https://api.anthropic.com}`). Merged because adding an interface method forces every implementer (incl. anthropic via the root registry) to satisfy it in the same commit to stay green. `Materialize` **kept** — `up` still uses it until 1.11, which deletes it. `identity` now imports `broker` (allowed: no slice/kernel rule broken, no cycle). TDD: fake + anthropic `Credential` tests (incl. error when no token) written failing first, then implemented.

### Harness abstraction (pod-side runtime)

- [x] **1.8** `internal/harness`: `Harness` interface — `Name()`, `Provisions() []string`, `Supports(vendor) bool`, `Env(brokerAddr, handle) → map[string]string` (renamed from `Materialize`: env-only, and avoids confusion with the `Provider.Materialize` removed at 1.11). `Registry` + `Get`, `FakeHarness`. Imports nothing from broker (strings only). TDD: fake Env/Supports/Provisions + registry tests written failing first, then implemented.
- [x] **1.9** `harness/claudecode`: `claudecode.Harness` — `Name()="claude-code"`, `Supports("anthropic")`, `Provisions()=["npm i -g @anthropic-ai/claude-code"]`, `Env()` → `ANTHROPIC_BASE_URL=<brokerAddr>` + `ANTHROPIC_AUTH_TOKEN=<handle>`. TDD (red→green). NOTE: `ANTHROPIC_AUTH_TOKEN`→Bearer and the npm/node image dependency are both verified/resolved at 1.14.

### Wire up `up` (remove the secret path)

- [x] **1.10** `up`: `--harness` flag (default `claude-code`), resolved/validated from a new `harness.Registry` param on `NewCmd`; unknown harness → error before any pod is created. `root.go` constructs `harness.Registry{"claude-code": claudecode.New()}`. Validate-only here; 1.11 uses the resolved harness. TDD: unknown-harness test + signature bump → red → green.
- [ ] **1.11** `up`: when `--identity` set → `provider.IsAuthenticated` (re-auth if stale) → `broker.Store(provider.Credential)` → `IssueHandle` → `harness.Materialize(brokerAddr, handle)` → fold env + `Provisions()` into the spec. **Delete the old `CLAUDE_CODE_OAUTH_TOKEN` injection**, and once `applyIdentity` no longer calls it, remove `Provider.Materialize` from the interface + `FakeProvider` + anthropic (orphaned after 1.6). Test with fake broker/provider/harness (assert: handle in env, real secret NOT in env).
- [ ] **1.12** `up`/`down` lifecycle: start the local broker gateway for the pod on `up`; `down` → `Revoke(handle)` + stop the gateway. Test the lifecycle (issue on up, revoked on down).
- [ ] **1.13** `root.go`: construct the broker + harness registry + provider registry once; inject into `up` and `identity`. `task ci` + smoke `up --help` / `identity --help`.

### Verify on the homelab (manual)

- [ ] **1.14** On a podman host: `poddle identity add work` (real `claude setup-token`) → `poddle up --harness claude-code --identity work` → confirm `claude` runs in the pod through the broker, and **no anthropic token is in the pod env** (`env | grep -i anthropic` shows only the handle + broker URL).

---

## Phase 2 — poddled + reattach (atomize when we start it)

- poddled service skeleton (unix socket, start/stop).
- Move the broker into poddled (persistent, host-side).
- Assigned-identity: pod-lifetime creds; close client, agent keeps running, reattach.
- Remote pods + reverse-tunnel egress from pod → broker.
- e2e tests for full flows (testcontainers): `up → agent calls through broker → down`, handle revoked on down, no secret in pod env — not just the broker in isolation.

## Phase 3 — Collaboration (coarse)

- `attach`/`detach`/`share`/`unshare`/`evict`; exclusive vs shared modes (handle lifecycle).
- Per-user delegated identities; per-driver creds + billing; per-user attribution/audit.

## Phase 4 — Cloud + enterprise (coarse)

- Multi-tenant broker with process-level walls; on-prem vs cloud.
- Cloud UI (pods · identities · audit · team); desktop app.
- Governance/compliance, SSO/SCIM, managed pods.

---

*Rule: never start a task before the previous one is green + committed. Keep the
old secret-inject `up --identity` working until 1.11 replaces it, so the tree
always builds.*
