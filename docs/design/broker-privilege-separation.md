# Broker privilege separation (Tier 2, roadmap)

**Status:** roadmap / not yet implemented. Tier 0 and Tier 1 (below) have
shipped. This document scopes Tier 2 so the design is settled before the work
starts.

Extends [`security-design.md`](../security-design.md) and
[`egress-lockdown-and-broker-placement.md`](./egress-lockdown-and-broker-placement.md).

## The surface we are hardening

The broker is the one process in the system that holds **every plaintext
credential** — and it is also the process that **parses untrusted, attacker-
influenced bytes**. Those bytes come from two directions:

- **Pod → broker (the forward proxy and the L4 terminators).** A malicious agent
  in the pod controls the `CONNECT` line, the `Proxy-Authorization` header, and
  the Redis/Postgres client handshake. `src/internal/broker/forward.go` and the
  L4 terminators parse all of it.
- **Upstream → broker (the reverse gateway and the redactor).** A compromised or
  hostile upstream controls the response headers and body that
  `src/internal/broker/gateway.go` proxies back and that
  `src/internal/broker/redactor.go` scans before it reaches the pod.

A memory-safety or logic bug in any of these parsers executes **in the same
address space that holds the vault**. Go's memory safety makes classic overflow
→ RCE unlikely, but the risk is real and broader than RCE: a parser bug that
mis-slices a buffer, a redactor gap, or a logic error that routes the wrong
credential can move plaintext secret bytes to the pod without any code
execution at all. Custody and parsing sharing one process is the structural
weakness. Tier 2 removes it.

## What already shipped (Tiers 0 and 1)

Tier 2 is worth doing only on top of the cheaper wins, which are done:

- **Tier 0 — fuzz the parsers and enforce the invariant.** The L4 parsers
  (Redis, Postgres, SCRAM) and the policy engine were already fuzzed. Added:
  - `FuzzRedactor_NeverEgressesManagedSecret` — a property fuzz asserting the
    core "no managed secret ever crosses to the pod" invariant: in `redact`
    mode no occurrence of the secret survives in the output outside a
    placeholder region; in `block` mode the request is rejected. This fuzz
    surfaced a real placeholder-boundary straddle subtlety (the redaction
    placeholder ends in byte `0xBB`, so a secret can re-appear spanning the
    placeholder's tail and adjacent *public* text) and pinned the correct,
    provenance-aware invariant against it. The discovered input is committed as
    a regression seed.
  - `FuzzProxyAuthToken` — the pod-controlled `Proxy-Authorization` parser must
    never panic (a crash on the sole egress is a DoS) and must never emit a
    token for a malformed header (which would let a pod impersonate another).
- **Tier 1 — least-privilege broker container.** `EnsureBroker`
  (`src/internal/podman/podman.go`) now runs the broker with `--cap-drop=all`,
  `--security-opt=no-new-privileges`, a `--read-only` rootfs, and a `--tmpfs`
  `/tmp`. Its only writable surfaces are the `/run/poddle` (control socket) and
  `/state` (audit db) mounts. This caps the blast radius of a parser bug at the
  OS level: no capabilities to abuse, no privilege escalation, no persistence on
  the rootfs. Pinned by `TestEnsureBroker_HardensContainer`.

Tiers 0 and 1 shrink the probability and the OS-level blast radius of a parser
bug. They do **not** change the fact that a successful in-process compromise
still sits next to the plaintext vault. That is Tier 2's job.

## Tier 2 — separate custody from parsing (OpenSSH-style privsep)

Split the single broker process into two cooperating processes with a hard
privilege boundary between them, modeled on OpenSSH's privilege separation:

- **The vault (monitor).** A small, audited process that holds the plaintext
  credentials and the audit-log writer. It parses **no** attacker-controlled
  bytes. Its entire input surface is a narrow, strongly-typed request/response
  protocol over a private, in-container socketpair. It is the only code with
  the credentials mapped into its address space.
- **The workers (unprivileged).** The forward proxy, the reverse gateway, the
  redactor, and the L4 terminators — everything that touches attacker bytes —
  run here, **without** any plaintext credential in their address space. A
  worker never sees a secret; it asks the vault to perform the
  credential-bearing step.

### The privilege boundary is the request protocol

The workers never *receive* a credential to splice; they hand the vault the
material the credential must act on, and the vault returns only the result. Two
shapes, matching the two ways poddle uses a secret today:

1. **Header/token injection (HTTP upstreams).** The worker sends the vault the
   outbound request line + headers (secrets absent) and the handle. The vault
   validates the handle against policy, injects the real `Authorization` (or
   equivalent), and returns the *signed/authorized* request bytes to write
   upstream. The worker writes them; the plaintext header existed only inside
   the vault for the duration of the call.
2. **Handshake delegation (Redis/Postgres/SCRAM).** The worker relays the
   opaque handshake frames; the challenge/response computation that needs the
   password runs in the vault, which returns only the response frame. The
   password never enters the worker.

Crucially, **the redactor moves into the vault side of the boundary** (or runs
as a vault-verified post-filter), so the "no secret egresses" check is made by
the process that actually knows the secrets — closing the theoretical gap where
a compromised worker could disable its own redaction.

```
  pod (untrusted)                         broker container
  ───────────────         ┌───────────────────────────────────────────┐
                          │  worker (unprivileged, no secrets mapped)   │
   HTTP_PROXY ───────────▶│  forward proxy · gateway · L4 · parsers     │
                          │            │  typed req/resp only  ▲        │
                          │            ▼  (socketpair)         │        │
                          │  ┌────────────────────────────────┴──────┐  │
                          │  │  vault / monitor (privileged)          │  │
   upstream ◀─────────────┼──│  plaintext creds · inject · handshake  │  │
                          │  │  redact-verify · audit-log writer      │  │
                          │  └────────────────────────────────────────┘  │
                          └───────────────────────────────────────────┘
```

### Why this is the right boundary

- A worker compromise (the likely target — it parses the hostile bytes) yields
  an address space with **no plaintext credentials in it**. The attacker can
  only speak the typed vault protocol, which is minimal, audited, and
  policy-checked on every call.
- The vault's input surface is a handful of typed messages from a *local*
  peer, not raw network/pod bytes — dramatically smaller and easier to fuzz to
  exhaustion than the HTTP/Redis/Postgres parsers.
- The control plane is already owner-only on a host socket; Tier 2 makes the
  **data** plane's secret custody equally unreachable from the parsing code,
  even after an in-container compromise.

## Cost, risks, and open questions

This is a real re-architecture, not a flag. Scoped honestly:

- **Latency.** Every brokered request gains one local socketpair round-trip to
  the vault. Expected sub-millisecond in-container, but it must be measured
  against the existing per-request budget, especially for the L4 splice path.
- **Handshake delegation complexity.** HTTP header injection is a clean fit for
  request/response delegation. The streaming L4 handshakes (SCRAM's multi-step
  challenge) need careful framing so the worker relays without ever needing the
  password — this is the hardest part and should be spiked first.
- **TLS-interception CA.** Interception (`egress = ... intercept`) generates a CA
  and signs leaf certs — a private-key operation that belongs on the vault side.
  This composes with the still-open CA-sharing gap noted in
  [`architecture.md`](../architecture.md#known-gaps--status); Tier 2 should land
  after, or together with, that fix so the CA private key lives only in the
  vault.
- **Process model.** Two processes in one container (a small supervisor, or the
  vault as PID 1 forking the worker) vs. the current single binary. Must remain
  compatible with the Tier 1 read-only, cap-dropped container and the
  static-binary packaging.

## Recommendation

Ship Tiers 0 and 1 now (done). Treat Tier 2 as a scoped follow-up gated on a
**SCRAM handshake-delegation spike** — if the password-bearing step can be
cleanly delegated over the socketpair without the worker ever holding the
password, the rest of the privsep split follows the established
request/response shape. Sequence it after the interception-CA fix so the CA
private key lands directly on the vault side.
