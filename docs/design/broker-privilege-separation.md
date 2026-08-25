# Broker privilege separation (Tier 2, roadmap)

**Status:** roadmap / not yet implemented. Tier 0 and Tier 1 (below) have
shipped. The **SCRAM handshake-delegation spike is done and passed** (the
`scramAuthenticator` seam is in `src/internal/l4/scram.go` today), so Tier 2 is
**unblocked** — what remains is the process split, not feasibility. This document
scopes it so the design is settled before the work starts.

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

## Spike: SCRAM handshake delegation (resolved — the gate is passed)

The hardest delegation — the multi-step SCRAM-SHA-256 challenge used by Postgres
— was spiked first, because if the streaming, password-bearing handshake could
*not* be split cleanly, the whole Tier 2 shape would be in doubt. It splits
cleanly.

**Finding.** In `src/internal/l4/scram.go` the password is consumed at exactly
one point: deriving `saltedPassword → clientKey → storedKey`. Everything before
it is parsing untrusted server bytes (salt, iteration count, combined nonce);
everything after it (`authMessage`, `clientSig`, the `proof` XOR) uses only
derived key material. So the entire password dependency collapses to one narrow,
typed call:

```go
// scramAuthenticator: the ONLY step that needs the password.
Proof(salt []byte, iter int, authMessage string) (proof []byte, err error)
```

The SCRAM **state machine** (the byte-parsing worker) parses `salt`/`iter`/nonce,
validates the nonce, and assembles the public `authMessage`; it then delegates
`Proof(...)`. The **authenticator** (the vault) holds the password, derives
`clientKey`/`storedKey` internally, and returns only the 32-byte proof. The
worker never holds the password *or* any reusable password-derived key — the
proof it receives is bound to this one `authMessage`, useless for replay (nonces
are per-exchange).

**Proven in code, today.** The seam is already in place as a behavior-preserving
refactor (`newSCRAMWithAuth`, `localSCRAMAuthenticator`):

- **Byte-identical.** The RFC 7677 known-answer proof (`TestSCRAM_RFC7677`) and
  the wire-level `TestPgSCRAM_HappyPath` are unchanged — delegation produces the
  same final message the in-process code did.
- **Confinement, asserted.** `TestSCRAM_PrivsepBoundary_ProofDelegatedWithoutPassword`
  routes the exchange through a spy that records everything crossing the boundary
  (salt, iter, authMessage — exactly what would travel to the vault) and asserts
  the password appears in none of it, while the golden proof still results.
- **Self-protecting vault.** `Proof` re-checks the PBKDF2 iteration bound, so a
  compromised worker can't force an unbounded loop even though the worker also
  bounds it — defense in depth on the password-holding side.

**Vault input surface stays tiny.** The vault receives a small salt, a bounded
int, and an opaque `authMessage` it only HMACs — it never parses protocol bytes.
That is the property that makes the vault side cheap to fuzz to exhaustion.

**md5 delegates the same way** — `md5Auth(user, pass, salt)` is a pure
`(user, salt) → response` function; it becomes a one-shot vault call, no state
machine. **Cleartext is the exception:** the protocol sends the password verbatim
to the upstream, so whoever writes the upstream socket (the worker) unavoidably
holds it for that write. The mitigation is a policy that can forbid cleartext
upstream auth (downgrade protection) and prefer SCRAM/md5 — a governance rule,
not a privsep mechanism.

**Conclusion:** the gate is passed. The password-bearing step delegates over the
socketpair as one `Proof` RPC per exchange with no protocol parsing on the vault
side, so the rest of the split (moving custody behind the boundary for HTTP
injection and the other L4 handshakes) follows the same request/response shape.

## Cost, risks, and open questions

This is a real re-architecture, not a flag. Scoped honestly:

- **Latency.** Every brokered request gains one local socketpair round-trip to
  the vault. Expected sub-millisecond in-container, but it must be measured
  against the existing per-request budget, especially for the L4 splice path.
- **Handshake delegation complexity.** ~~The hardest part.~~ **Resolved by the
  SCRAM spike above.** HTTP header injection and md5 are one-shot `(inputs) →
  result` calls; SCRAM's multi-step challenge splits into a single `Proof(salt,
  iter, authMessage)` delegation with no protocol parsing on the vault side. The
  seam exists in code today (behavior-preserving). Cleartext is the one auth mode
  that can't hide the password from the socket-writing worker — forbid it by
  policy and prefer SCRAM/md5.
- **TLS-interception CA.** Interception (`egress = ... intercept`) generates a CA
  and signs leaf certs — a private-key operation that belongs on the vault side.
  The CA-sharing gap this was sequenced behind is now **fixed** (the broker
  persists and signs from its bind-mounted `/state/egress-ca`, which `up` reads);
  Tier 2 should keep that CA private key on the vault side of the split, exposing
  only a `SignLeaf(host) → cert` call to the worker.
- **Process model.** Two processes in one container (a small supervisor, or the
  vault as PID 1 forking the worker) vs. the current single binary. Must remain
  compatible with the Tier 1 read-only, cap-dropped container and the
  static-binary packaging.

## Recommendation

Tiers 0 and 1 are shipped. The **SCRAM handshake-delegation spike is done and the
gate is passed** (section above): the password-bearing step delegates cleanly as
one `Proof` RPC per exchange, proven in code and byte-identical to the current
path. The interception-CA prerequisite is also fixed. Tier 2 is therefore
**unblocked**.

The remaining work is the process split itself, not further feasibility. Next
concrete steps, in order:

1. **Define the vault protocol** — the small typed message set: `SCRAMProof`,
   `MD5Response`, HTTP `InjectAuth(request) → authorized-request`, `SignLeaf(host)
   → cert`, and the redact-verify call. Each is a pure `(inputs) → result` shape
   the spike validated for SCRAM.
2. **Stand up the two-process model** — vault as PID 1 forking the worker (or a
   small supervisor), a socketpair between them, compatible with the Tier 1
   read-only, cap-dropped container and static-binary packaging.
3. **Move custody behind the boundary** — the vault holds credentials, the CA
   private key, and the redactor; the worker keeps the parsers and holds no
   secret. The `scramAuthenticator` seam already in the code becomes the
   worker-side RPC client with no state-machine changes.
4. **Measure the added latency** — one socketpair round-trip per auth/injection
   against the per-request budget, especially on the L4 splice path.
