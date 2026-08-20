# poddle - Tasks

Work one at a time, top to bottom. Each task is a small TDD unit: write the test,
make it pass, `task ci` green, commit. Never start a task before the previous one
is green and committed.

## Phase 1 - Harness + secretless broker (shipped)

Complete: in-memory broker (vault + revocable handles + injecting gateway,
memguard-sealed secrets, handle TTL), secretless `up --identity` / `--harness`,
the `claude-code` harness, the `anthropic` provider, templates (`--template`,
`extends`, repo clone), connectors (declarative git/CI/registry tokens), and
interactive identity selection. Verified end-to-end against real podman and the
real Claude Code CLI (mock upstream, sentinel secret): the pod only ever holds a
handle, and the upstream only ever sees the real credential.

## Phase 2 - poddled + reattach (shipped)

- [x] poddled service skeleton (unix socket, start/stop).
- [x] Move the broker into poddled (persistent, host-side).
- [x] Assigned identity: pod-lifetime creds. Close the client, the agent keeps
      running, reattach later (`up --detach` + `attach`; handles live until `down`).
- [x] Remote pods via podman's `--url ssh://` transport (a pod on a remote host).
- [ ] Reverse-tunnel egress from a remote pod back to the broker — still pending;
      the remote harness/e2e is stubbed.
- [x] Full-flow e2e (real podman): `up` -> agent calls through broker -> `down`;
      handle revoked on down; no secret in pod env.

Also shipped in this phase, beyond the original list: the **L4 datastore broker**
(a pod reaches real Redis/Postgres with only a handle — the broker re-auths with
the real credential, incl. Postgres SCRAM/md5, and splices), a **tamper-evident
audit log** (`daemon audit`, hash-chain `--verify`), the **forward proxy** that
governs a pod's arbitrary egress by policy, and **dynamic vertical sizing**
(`resize`, reactive autoscale, `move` live-migrate). A local **observability
dashboard** (audit stream, policy, destinations) is in active development.

## Phase 3 - Collaboration

- [ ] `attach` / `detach` / `share` / `unshare` / `evict`; exclusive vs shared
      modes (handle lifecycle).
- [ ] Per-user delegated identities: per-driver creds, billing, attribution/audit.

## Phase 4 - Cloud + enterprise

- [ ] Multi-tenant broker with process-level walls; on-prem vs cloud.
- [ ] Cloud UI (pods, identities, audit, team); desktop app.
- [ ] Governance/compliance, SSO/SCIM, managed pods.

## Distribution / install methods

- [x] Renamed the Go module path to `github.com/datadir-lab/poddle` (2026-08-19):
      `go install github.com/datadir-lab/poddle/src@latest` is the blessed path.
      Scrubbed the old Forgejo path from all history, re-signed every commit, and
      re-released v0.1.0 with clean binaries/SBOMs.
- [ ] Publish install methods beyond `go install`: a Homebrew tap, a Scoop
      manifest, and a `curl | sh` installer (goreleaser can produce all three).
