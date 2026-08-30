# Security design and cryptography

This document records poddle's secure-design posture and its cryptography
inventory. It is the canonical reference for the security questions in our
[OpenSSF Best Practices self-assessment](./openssf-best-practices.md) and for
anyone reviewing how poddle handles secrets. For how to *report* a vulnerability,
see [SECURITY.md](../SECURITY.md).

## Threat model

poddle's whole purpose is to run an untrusted coding agent without giving it real
credentials. The design assumptions:

- **The pod is untrusted.** A coding agent (and any code it runs) inside a pod
  may be adversarial. It must never obtain a real credential.
- **The broker is trusted** and runs on the user's own host. It holds the real
  credentials, injects them per request, and stays outside the pod's reach.
  (Defense in depth: `PODDLE_BROKER_PRIVSEP` further splits the broker so a bug in
  its untrusted-byte-parsing surface cannot reach the credential custody — see
  *Privilege separation* below.)
- **The network is hostile.** All brokered upstreams are reached over TLS; the
  pod holds only opaque, revocable *handles*, never secrets.

The core invariant — *no real secret is ever present inside a pod* — is enforced
in code and guarded by architecture and end-to-end tests (`task arch`, `task
e2e-*`; see [TESTING.md](../TESTING.md)).

## Secure-design principles applied

- **Secretless by construction.** The pod receives a handle; the broker swaps it
  for the real credential on the wire (`src/internal/broker/gateway.go`). HTTP
  services get header injection; databases (Postgres, Redis) are terminated at
  the broker, which performs the real authentication handshake and splices the
  sockets so the password never reaches the pod.
- **Least privilege / fail-closed mounts.** `block_paths` refuses to create a pod
  that would mount host secret stores (`~/.ssh`, `~/.aws`, poddle's own token
  store); bind mounts are scanned for credentials (`secret_scan = warn | block`).
- **Defense in depth on egress.** The gateway redacts the broker's managed
  secrets plus high-confidence secret shapes (private keys, `AKIA…`, `ghp_…`)
  from outbound bodies, and can block on detection (`egress = redact | block |
  off`, `src/internal/broker/redactor.go`). It also scrubs the injected secret
  back out of a reflecting upstream's *response*, so an upstream that echoes the
  credential (a debug/echo route, a verbose error, an MCP tool mirroring input)
  cannot bounce it into the pod. Every brokered request is recorded in a
  tamper-evident audit log.
- **Cloud-metadata SSRF floor.** The egress forward proxy refuses any connection
  whose *resolved* address is cloud instance-metadata / link-local (IMDS
  `169.254.169.254`, `169.254.0.0/16`, `fe80::/10`, AWS IPv6 `fd00:ec2::254`) —
  at dial time (defeating a hostname or rebinding record aimed at the metadata
  IP) and regardless of policy, so untrusted pod code can't reach IMDS to steal
  instance credentials even under an allow-all pod (`src/internal/broker/forward.go`;
  opt out with `PODDLE_ALLOW_LINK_LOCAL=1`).
- **Revocation.** `poddle down` revokes a pod's handles; access dies immediately
  because the broker stops honoring them.
- **Privilege separation (opt-in).** The broker's container is already hardened
  (`--cap-drop=all`, `no-new-privileges`, read-only rootfs, distroless static
  binary). Beyond that, `PODDLE_BROKER_PRIVSEP=1` splits the broker into two
  processes: an untrusted *front* that parses pod and upstream bytes (the gateway,
  forward proxy, and L4 terminators) and a privileged *keeper* subprocess that
  holds the only copy of the credential custody — the memguard-sealed vault, OAuth
  refresh tokens, and the TLS-interception CA key — reached over an `AF_UNIX`
  socketpair. A memory-disclosure bug in the parsing front then cannot read the
  custody; the front holds only revocable handles and per-request injected material.
  It is opt-in and default-off, fail-closed on keeper death, and Linux-only. Design:
  [`design/broker-privilege-separation.md`](./design/broker-privilege-separation.md);
  operator-facing summary in [`architecture.md`](./architecture.md).

## Cryptography inventory

poddle does not invent cryptographic primitives. It uses well-reviewed,
standard protocols implemented with Go's standard library and
`golang.org/x/crypto`; all of it is FLOSS and reproducible with FLOSS tools.

| Concern | What poddle uses | Where |
|---|---|---|
| Transport / MITM resistance | TLS via Go `net/http` + `crypto/tls` (TLS 1.2+ by default), to HTTPS-only upstreams; no `InsecureSkipVerify` | `src/internal/broker`, `src/internal/connector` |
| Egress-interception CA (opt-in) | Self-signed ECDSA P-256 CA minting short-lived per-host x509 leaves for policy-opted-in TLS interception; the CA key stays broker-side (keeper-side under privsep), never in the pod | `src/internal/tlsca/tlsca.go` |
| Database auth (Postgres) | SCRAM-SHA-256 (RFC 5802 / RFC 7677), PBKDF2-SHA-256, HMAC-SHA-256 | `src/internal/l4/scram.go`, `postgres.go` |
| Database auth (Redis) | Redis `AUTH` over the broker-terminated connection | `src/internal/l4/redis.go` |
| Secure randomness | `crypto/rand` for handles and SCRAM client nonces | `src/internal/broker/handles.go`, `src/internal/l4/postgres.go`, `src/internal/poddled/daemon.go` |
| In-memory secret protection | memguard (`github.com/awnumar/memguard`) guarded enclaves; BLAKE2b via `golang.org/x/crypto` | broker credential vault |
| Release integrity | cosign keyless signature over `checksums.txt` + SLSA provenance (`*.intoto.jsonl`) | `.github/workflows/release.yml`, `.goreleaser.yaml` |

Notes that map to the OpenSSF crypto criteria:

- **Published protocols only** (`crypto_published`, `crypto_working`,
  `crypto_weaknesses`): SCRAM-SHA-256 and TLS are publicly specified and
  reviewed. No MD5/SHA-1-based or otherwise-broken constructions are used for
  security purposes.
- **Delegated to libraries** (`crypto_call`, `crypto_floss`): primitives come
  from the Go standard library and `x/crypto`; the only bespoke code is the
  SCRAM *protocol driver*, which composes stdlib HMAC/SHA-256/PBKDF2.
- **Key length** (`crypto_keylength`): SCRAM-SHA-256 derives a 32-byte key; TLS
  suite and key selection follow Go defaults, which meet current NIST minimums.
- **Randomness** (`crypto_random`): all security-relevant random values use
  `crypto/rand`, never `math/rand`.
- **Password storage** (`crypto_password_storage`): *not applicable* — poddle
  authenticates no end-users and stores no user passwords. It brokers *service*
  credentials, which live only in memory (memguard) on the trusted host and are
  never written to disk. The one at-rest secret, a client-side identity token,
  is written with owner-only `0600` permissions
  (`src/internal/identity/anthropic/anthropic.go`) and is scoped to the user's
  own machine.
- **DoS hardening**: the SCRAM client caps the server-supplied PBKDF2 iteration
  count at 2^20 to bound work from a hostile or misbehaving server
  (`maxSCRAMIterations`, `src/internal/l4/scram.go`).

## Knowledge of common vulnerabilities

Maintainers apply standard secure-coding practice for this class of tool:

- **Input parsing is fuzzed.** The wire parsers most exposed to hostile input —
  the Postgres and Redis L4 handlers, the SCRAM exchange, and the egress policy
  engine — have Go native fuzz targets (`src/internal/l4/*_fuzz_test.go`,
  `src/internal/policy/policy_fuzz_test.go`), run continuously.
- **Secrets stay out of logs and egress** by design (redaction + audit above).
- **Static and dependency analysis run in CI**: CodeQL (SAST), golangci-lint,
  `govulncheck`, osv-scanner, and Dependabot. See
  [`.github/workflows`](../.github/workflows).

## References

- Architecture and boundaries: [docs/design/](./design)
  (`secretless-identity.md`, `stateless-pods-and-move.md`, `open-core.md`, …)
- Reporting: [SECURITY.md](../SECURITY.md)
- Test tiers: [TESTING.md](../TESTING.md)
