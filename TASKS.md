# poddle — Atomic tasks

Work **one at a time, top to bottom** — not in parallel. Each task is a small
TDD unit: write the test, make it pass, `task ci` green, commit. Check it off.

---

## Phase 1 — Harness + secretless broker (local, single-user)

### Broker

- [ ] **1.1** `internal/broker`: define `Credential{Mode,Vendor,Secret,BaseURL}` and `Handle{Value,Tenant,CredID,Scope}` types. Test: construct + zero-values.
- [ ] **1.2** broker: in-memory `Vault` — `Store(tenant, cred) → credID`, `Get(tenant, credID)`, `Delete`. Test: store/get; get across a different tenant is denied.
- [ ] **1.3** broker: `IssueHandle(tenant, credID, scope) → Handle` (random, high-entropy) + `Resolve(handleValue) → (Credential, ok)` + `Revoke(handleValue)`. Test: resolve returns the cred; revoked handle no longer resolves; cross-tenant resolve denied.
- [ ] **1.4** broker: injecting HTTP handler — reads the handle from the incoming `Authorization`, resolves it, rewrites `Authorization` to the real secret, reverse-proxies to the credential's upstream base URL. Test with an `httptest` upstream: assert the upstream saw the real auth + the original body/path.
- [ ] **1.5** broker: `Serve(addr)` / `Stop()` returning the bound addr. Test: start, one request round-trips through to a fake upstream, stop.

### Provider (auth) — refactor to hand a Credential, not a secret env

- [ ] **1.6** `identity.Provider`: replace `Materialize()` with `Credential(id) → broker.Credential`. Update `FakeProvider`. Test the fake.
- [ ] **1.7** `identity/anthropic`: implement `Credential()` (stored token → `Credential{Mode:"subscription", Vendor:"anthropic", Secret:token, BaseURL:"https://api.anthropic.com"}`); delete `Materialize`. Test.

### Harness abstraction (pod-side runtime)

- [ ] **1.8** `internal/harness`: `Harness` interface — `Name()`, `Provisions() []string`, `Supports(vendor) bool`, `Materialize(brokerAddr, handle) → sandbox env`. Add a fake. Test the fake + a registry.
- [ ] **1.9** `harness/claudecode`: `Provisions()=["npm i -g @anthropic-ai/claude-code"]`, `Supports("anthropic")`, `Materialize()` → `ANTHROPIC_BASE_URL=<brokerAddr>` + `ANTHROPIC_AUTH_TOKEN=<handle>`. Test.

### Wire up `up` (remove the secret path)

- [ ] **1.10** `up`: add `--harness` flag; resolve it from the harness registry (default `claude-code`). Test.
- [ ] **1.11** `up`: when `--identity` set → `provider.IsAuthenticated` (re-auth if stale) → `broker.Store(provider.Credential)` → `IssueHandle` → `harness.Materialize(brokerAddr, handle)` → fold env + `Provisions()` into the spec. **Delete the old `CLAUDE_CODE_OAUTH_TOKEN` injection.** Test with fake broker/provider/harness (assert: handle in env, real secret NOT in env).
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
