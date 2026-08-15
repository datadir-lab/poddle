# Secretless identities, harnesses & the credential broker

**Design · 2026-08-16**

## Principle

**A vendor secret (OAuth token, API key) never lives in a pod.** The pod holds
only a *scoped, revocable handle* to a broker. The broker holds the real creds
(client-side), injects them per-request on the way to the vendor, attributes
each request to a user, and can revoke a handle instantly. Baking a token into
a pod's env is the thing we refuse to do — because the moment a teammate can
`attach`, a baked token is a credential leak.

This replaces the current `up --identity` behaviour (which injects
`CLAUDE_CODE_OAUTH_TOKEN` into the pod env) with a broker handle.

## Three roles (unchanged split, secret removed)

- **Provider** — auth vendor, **client-side** (`anthropic`, `openai`, `local`).
  Owns `Authenticate` (login) and `IsAuthenticated` (the ping). It hands its
  credential to the **broker**, never to the pod.
- **Harness** — runtime, **pod-side** (`claude-code`, `codex`, `pi`, `aider`).
  Owns `Provisions()` (install) and `Materialize()` — which now points the
  harness at the broker (`*_BASE_URL` + the handle) instead of a real secret.
- **Broker** — **client-side (later: poddled)**. Holds creds, runs an egress
  gateway the pod calls, issues/revokes handles, injects the real cred, and
  logs attribution.

The **Credential** stays in the broker; the pod only ever sees a **Handle**.

## Data flow — single user

```
poddle up --harness claude-code --identity work
  broker.Register(anthropicProvider.Credential(work))   # broker holds the real cred (client-side)
  handle := broker.IssueHandle(pod, user)               # scoped to this pod, revocable
  pod env: ANTHROPIC_BASE_URL = <broker-addr>
           ANTHROPIC_API_KEY  = <handle>                # a capability, NOT the vendor secret
  # pod reaches the broker: localhost (local) or a reverse tunnel over the engine conn (remote)

agent-in-pod --(request bearing handle)--> broker
  broker: handle -> session/user -> inject REAL Authorization -> vendor API -> log(who, what)

poddle down / detach:  broker.Revoke(handle)            # pod access dies; nothing to leak
```

The pod never had the anthropic token. Steal the handle and it's worthless off
the broker, and dead the moment the pod is torn down.

## Data flow — shared pod (team)

```
poddle share <pod> --with bob        # authZ: bob may attach
bob: poddle attach <pod>
  broker.IssueHandle(pod, bob)       # a per-SESSION handle bound to BOB's identity
  bob's agent session -> handle -> broker injects BOB's creds -> attributed to bob
```

- **Per-user handles → per-user creds → per-user attribution.** Alice's actions
  bill Alice and log as Alice; Bob's as Bob. The pod holds neither secret.
- **Per-user agent sessions**: a single agent process reads one credential, so
  multi-user auth means each attacher runs *their own* agent session (shared
  filesystem/workspace, separate auth + attribution). One shared agent = one
  shared identity = back to the leak; we don't do that.

## The Handle

- Opaque, random, high-entropy; bound to `{pod, user, provider, scope}`.
- **Revocable** (tear-down, detach, timeout) and short-TTL with broker-side
  refresh — so a captured handle has a tiny, bounded window.
- Presented by the pod exactly where the harness expects a key
  (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, …) so no harness changes are needed.

## The Broker = an identity-aware LLM egress gateway

It's the Vernir gateway, scoped to auth for now:
1. Terminates the pod's request (to `*_BASE_URL`), authenticated by the handle.
2. Maps handle → session → the right vendor **Credential**.
3. Injects the real `Authorization` and forwards to the vendor.
4. Logs `{who, pod, model, when}` → attribution / audit.
5. Enforces revocation + TTL.

**Deployment:** local single-user = an **ephemeral in-CLI broker** (poddle runs
a localhost proxy for the pod's lifetime). Remote / team = the **poddled**
broker (one place that holds creds, routes many pods/users). Same interface;
this is the reason poddled exists.

## Harness / mode compatibility (honest)

- **API-key mode** (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `*_BASE_URL`): clean.
  This is the proven LLM-gateway pattern (LiteLLM/Portkey) — the pod gets a
  gateway handle, the gateway injects the real key. Fully secretless. ✅
- **Subscription/OAuth mode** (`CLAUDE_CODE_OAUTH_TOKEN`): needs verification —
  subscription traffic may not route through `*_BASE_URL`. Fallbacks: (a) run
  subscription requests through the broker if the harness honours base-url in
  that mode; (b) if not, the broker mints/rotates a **short-TTL** token as the
  handle (not fully secretless, but revocable + bounded, not a long-lived
  token). Decide per harness. ⚠️
- **Local LLM**: trivially secretless (endpoint; the broker proxies, or the pod
  points straight at the local model). ✅

## authZ (for `share`)

- The broker owns "who may attach to which pod" and "whose creds a session
  uses." Local single-user: trivial (owner only). Team: poddled + a team model.
- `poddle share <pod> --with <user>` grants attach; each attach gets its own
  identity-bound handle.

## Refactor from what's built

1. `identity.Provider`: replace `Materialize()` (returns pod env) with
   `Credential()` (returns the raw cred to the broker only).
2. New `broker` package: `Register(cred)`, `IssueHandle(pod,user) → Handle`,
   `Revoke(handle)`, plus the gateway (`serve`), starting as a localhost proxy.
3. New `Harness` abstraction + `claude-code` harness: `Provisions()`,
   `Materialize(brokerAddr, handle) → env` (base-url + handle, never a secret).
4. `up`: `broker.Register(provider.Credential(id))` → `IssueHandle` →
   `harness.Materialize(brokerAddr, handle)` → inject handle+base-url → on
   `down`, `Revoke`.
5. `attach`/`share`: per-user `IssueHandle`, per-user agent session (team tier).

## Risks / open decisions

- **Broker complexity from day 1** — even local single-user now runs a proxy.
  Accepted: it's the point (no baked secrets ever).
- **Subscription-through-base-url** unverified per harness — verify on the
  homelab; fall back to short-TTL rotating handle where it doesn't route.
- **Remote reachability** — the pod must reach the broker; needs a reverse
  tunnel over the engine/ssh connection.
- **Broker = crown jewel** (holds all creds) — self-hosted, hardened, client or
  poddled only; the same concentration-of-risk noted in the governance spec.
- **Per-user agent sessions** for shared pods — UX + harness support to design.
