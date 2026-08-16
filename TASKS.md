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

### Composition + broker facade

- [x] **1.A** `internal/app`: `App` composition struct (`Engine`, `Identities`, `Providers`, `Harnesses`) built once in `root` and injected into every command. Replaces the growing per-command param lists; commands drop their narrow interfaces and read `a.*`. Test fakes satisfy `engine.Engine` via the embedded-interface trick. In `internal` (not `main`/`cli/app`) so slices can import it without breaking slice-independence. **The broker is deliberately NOT on App** — it's up-scoped in Phase 1, poddled-owned in Phase 2 (see 1.B). TDD: all four command tests flipped to `*app.App` → red → green.
- [x] **1.B** `broker.Broker` facade composing `Vault`+`Handles`+`Server` (tenant `"local"`, hidden from callers): `NewBroker`, `Serve(addr)→bound`, `Addr` (also added to `Server`), `Store(cred)→credID`, `IssueHandle(credID,scope,ttl)`, `Revoke`, `Stop(ctx)`. Constructed **inside `up`**, not on App. TDD (red→green, race-clean): store/issue/resolve, revoke, addr-empty-until-serve, serve→handle round-trip→stop.

### Wire up `up` (remove the secret path)

- [x] **1.10** `up`: `--harness` flag (default `claude-code`), resolved/validated from the harness registry; unknown harness → error before any pod is created. Validate-only here; 1.11 uses the resolved harness. TDD.
- [x] **1.11** `up` secretless flow: `--identity` → `IsAuthenticated` (re-auth if stale) → `provider.Credential` → `harness.Supports(cred.Vendor)` (else error) → `broker.Store` → `IssueHandle(credID, podName, 0)` → `harness.Env(broker.Addr, handle)` → fold env + `Provisions()` into `spec` (added `Spec.Setup []string`). Deleted the `CLAUDE_CODE_OAUTH_TOKEN` injection **and** `Provider.Materialize` + the orphaned `identity.Materialization`/`identity.Mount` types. Used a **real** `*broker.Broker` (not a fake) — `up.NewCmd(a, b)`, in-memory Store/Issue, no serve needed. TDD: identity tests rewritten (handle in env, secret absent, provisions in Setup) → red → green. Podman actually running `Setup` is wired before 1.14. (Also gofmt-fixed app.go/anthropic.go/sandbox.go.)
- [x] **1.12** `up` lifecycle (Phase-1, up-scoped): when `--identity` set → `Serve` the broker (loopback for now) → wire → create → attach; **`Revoke(handle)` + `Stop` on session end** (deferred, revoke-before-stop). **`--detach` + `--identity` → error** (points to poddled/Phase 2). Introduced a `credBroker` seam interface on `up.NewCmd` (real `*broker.Broker` satisfies it; a **spy** asserts the exact lifecycle order `serve→store→issue→create→attach→revoke→stop`). Env base URL now `http://<addr>`. Cross-process revoke-on-`down` stays deferred to poddled. TDD (spy + detach-error → red → green). NOTE: the fmt gate caught this commit's unformatted test — working as intended.
- [x] **1.13** finalize `root`/`App` wiring — most of it landed incrementally (App in 1.A, harnesses in 1.10, broker in 1.11). Strengthened `root_test` to assert the composition root registers all four subcommands; refreshed the `NewRootCmd` doc. `task ci` green; `up`/`identity --help` smoke ok.

### Make it run on real containers (pre-1.14)

- [x] **1.C** podman: run `spec.Setup` after create — `podman exec <id> sh -c "<cmd>"` per command (local + remote via `--url`); on failure leave the container running and return an error naming the pod for cleanup. TDD: happy path (exec calls in order) + setup-failure via a local stub runner (the shared `exec.Fake` can't fail one call but not another).
- [x] **1.D** broker reachability from the pod: broker binds `0.0.0.0:0`; the pod env's base URL is `http://host.containers.internal:<port>` (port extracted from `Addr()` via `net.SplitHostPort`), not the loopback bind. `host.containers.internal` is a const in `up` for now (option A — graduates to an engine capability when a 2nd backend lands). `0.0.0.0` is LAN-exposed but handle-gated; Phase 2 binds tighter. TDD (spy Addr → `host.containers.internal:12345` → red → green).

### Verify

- [x] **1.14a — automated (`task e2e-claude`, needs docker).** `TestE2E_Secretless_RealClaudeCode` (`src/internal/broker`, `//go:build e2e`): runs the **real Claude Code CLI** in a node:22 container → a **real broker** holding a sentinel secret with a **mock Anthropic upstream**. Asserts the upstream saw `Authorization: Bearer <sentinel>` and **never the handle**, and `claude -p` returned `"works"` (exit 0). Empirically proves — no real account/OAuth — the four things 1.14 was for: `ANTHROPIC_AUTH_TOKEN`→`Authorization: Bearer` (verbatim), `ANTHROPIC_BASE_URL` routes all traffic (+`/v1/messages`), the handle→secret swap on the wire, and headless claude through the gateway. **Both "unknowns" resolved.** (Confirmed live 2026-08-16, 20s.) **Also wired into Woodpecker CI** (`woodpecker/e2e-claude.yaml`, manual): runs on the step's nested podman via `host.containers.internal`, so it also confirms podman reachability — passed green in CI (pipeline #49, ~11s). Engine is configurable (`PODDLE_E2E_CONTAINER_CLI`: docker locally, podman in CI).
- [x] **1.14b — full podman orchestration. AUTOMATED** via `TestE2E_Up_Secretless` (`tests/e2e`, `task e2e-up`, `woodpecker/e2e-up.yaml`, nested podman): drives the **real `poddle up --identity --exec`** binary — sentinel identity in a throwaway `XDG_CONFIG_HOME`, upstream pointed at a mock via `PODDLE_ANTHROPIC_BASE_URL`, `--exec 'claude -p …'`. Asserts claude returned "works" through the broker, the mock saw `Bearer <sentinel>` and never a `poddle_` handle, and the pod env carried only the handle. Exercises pod create + `Setup` npm-install-in-pod + broker lifecycle + `host.containers.internal` under real podman. **Passed green in CI (pipeline #54, 25s).** Enabled by `up --exec` + the `PODDLE_ANTHROPIC_BASE_URL` override. The manual runbook below is kept only as a reference for a real podman box:
  1. `poddle identity add work` (real `claude setup-token`, browser login) → `poddle identity status work` = authenticated.
  2. `poddle up mybox --identity work --harness claude-code --image node:22` — **use a node image**: the default `debian:stable-slim` has no npm, so the `npm i -g @anthropic-ai/claude-code` Setup step would fail.
  3. In the attached pod: `env | grep -i anthropic` shows **only** `ANTHROPIC_BASE_URL=http://host.containers.internal:<port>` and `ANTHROPIC_AUTH_TOKEN=poddle_…` (the handle) — **no real token anywhere**.
  4. Isolate reachability from auth: `curl "$ANTHROPIC_BASE_URL/"` from the pod → expect `401 invalid or revoked handle` (proves the pod can reach the broker + the gateway is alive).
  5. Run `claude` → it should work through the broker.
  - **What's left to confirm here** (1.14a already proved the Bearer swap + claude): only podman-specific bits — that `spec.Setup`'s `npm i` actually installs claude-code in the pod, and that `host.containers.internal` routes to the host's `0.0.0.0` bind under this podman config (rootless slirp/pasta vs rootful bridge — the curl in step 4 isolates it).
  - Teardown: exit the session → `up` revokes the handle + stops the broker; then `poddle down mybox`.

### Interactive UX

### Templates (env blueprints)

- [x] **1.T** `internal/config` + `up --template` + repo clone. Reusable pod blueprints (TOML, one file = one template named by filename): `image/size/harness/identity/repo/env/mounts/setup/scripts`. Compose via **`extends`** (string or list) — scalars override, lists append, maps merge; diamonds resolve, cycles error — across `~/.config/poddle/templates/`, project `.poddle/<name>.toml` (shadows user), and the root `.poddle.toml` **default**. CLI flags override the template (`flagOr` precedence). **`scripts`** reference `.sh` files (paths relative to the config's dir; read + run in the pod, after inline `setup`). **`repo`** → `git clone <repo> /workspace` first (private-repo auth arrives with broker'd git tokens). Lazy `config.DirResolver` (a broken project file fails only `up`, not `ls`). TDD: 10 config-resolution tests + up apply/CLI-override/repo-clone. **E2E green in CI** (`TestE2E_Templates`, `task e2e-templates`, pipeline #64, ~58s): `nested_default_full` (3-level extends + scripts + repo clone + identity + claude-code installed + runs through the broker) and `named_template` (`--template` picks a different image).

### Connectors (secretless git/CI)

- [x] **1.C1–C4** Broker'd service tokens (git/CI) — the secretless model past the LLM. **Connector** = a *declarative* type (a TOML Definition: inject `mode` + pod-wiring `env`/`setup` templates with `{broker}`/`{handle}`/`{base_url}` placeholders), so a new service is a few lines of TOML, no code. **Connection** = a named, owner-scoped instance (`Connection : Connector :: Identity : Provider`; owner→broker tenant). **Built-in defs (5):** forgejo/github/gitlab (`basic` + git `url.insteadOf`; github/gitlab default `base_url`, HTTP pod→broker + HTTPS broker→host, no TLS interception), npm (`bearer` + `.npmrc` registry/token), woodpecker (`bearer` + env). A `Definition` can carry a default `base_url` (public services need only a token; a connection's URL overrides). A user file overrides/adds. **L1** gateway `basic` mode (handle from Basic-username, inject `Basic user:token`) + `WWW-Authenticate` challenge (git doesn't send Basic preemptively). **L2** declarative engine (`internal/connector`: Definition/Connection/Credential/Wiring) + `Store`. **L3** `poddle connect add/ls/rm` (token via stdin) + template `connectors = [...]` + `up` (serve broker for identity AND/OR connectors; connector setup prepended before the repo clone; `--detach`+any-brokered-cred → poddled error). **L4 e2e green in CI** (`TestE2E_Connector_GitBasic`, pipeline #72, ~31s): real `poddle up` → `git clone` through the broker → mock saw `Basic base64(me:sentinel)`, never a `poddle_` handle. Woodpecker is a `connect` + a `connectors` entry away (built-in def, no new code).

### Interactive UX

- [x] **1.E** `poddle up` without `--identity` on a TTY: arrow-key **select** an identity (`charmbracelet/huh`) — existing identities + "➕ Add a new identity" + "None — plain sandbox" (shown even with one). Add-new → provider select → name → auth, then it's used. Behind an `internal/prompt.Prompter` seam: real huh impl wired in `root` only when `x/term.IsTerminal` (else `App.Prompter` is nil), `FakePrompter` in tests. Non-TTY/`--detach`/explicit `--identity` skip the prompt (scripts/CI never hang). TDD: select-existing / add-new / none / detach-no-prompt / explicit-no-prompt.

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
