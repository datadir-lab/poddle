# Running poddle on your own cloud VM

Scripts for standing up a **single-user** poddle host on a cloud VM, plus what
was actually verified and what to watch out for.

```
setup.sh           provision + build + verify, idempotent
poddle-doctor.sh   diagnose (and recover from) a broker that returns 502
```

## The shape that works

**poddle installed *on* the VM, with you SSH'd into it.** Everything — CLI,
daemon, broker, pods — runs on that one machine.

The other shape people ask for — poddle on your laptop driving pods on a remote
box via `PODDLE_HOST=ssh://…` — **is not finished.** `PODDLE_HOST` redirects only
*podman*; the broker is launched against local podman unconditionally
(`src/internal/poddled/client.go`, `podman.New(exec.OS{}, "")`) and the CLI talks
to poddled over a local Unix socket. Your pods would land on the remote host
while the broker stayed on your laptop, and the pods would reach for
`host.containers.internal` where nothing is listening. The `PODDLE_BROKER=
colocated|<addr>|tunnel` placement knob that fixes this is specified in
`docs/design/egress-lockdown-and-broker-placement.md` but not implemented — only
step 1 of 4 shipped. The remote e2e test (`tests/e2e/remote_loopback_test.go`)
ssh's to `127.0.0.1`, so the pods are local and the broker is reachable; the
genuinely-remote path has no passing test.

## Quick start

On a fresh Ubuntu 24.04 VM, as your normal (non-root) sudo user:

```bash
curl -fsSLO https://raw.githubusercontent.com/datadir-lab/poddle/main/deploy/cloud-vm/setup.sh
chmod +x setup.sh && ./setup.sh
```

Then open a new shell and:

```bash
poddle identity add work --provider anthropic     # token on stdin, never argv
echo "$GITHUB_PAT" | poddle connect add gh --connector github
poddle up
```

To deploy an unpushed or patched tree, rsync it up and set `PODDLE_SRC_LOCAL=1`:

```bash
rsync -a --exclude .git ./poddle/ vm:~/poddle-src/
ssh vm 'PODDLE_SRC=~/poddle-src PODDLE_SRC_LOCAL=1 ./setup.sh'
```

Other knobs: `GO_VERSION`, `PODDLE_REPO`, `PODDLE_REF`, `BROKER_IMAGE`, `BIN_DIR`.

## Why it builds from source

The published `ghcr.io/datadir-lab/poddle-broker` image is still **private** — an
anonymous manifest pull returns 403, and `docs/architecture.md` lists this under
*Known gaps*. Because `poddle up` starts the broker whenever you use an identity,
a connector, **or** a policy (`src/cli/up/command.go`), every useful command
fails on a clean host until that image is reachable.

So `setup.sh` builds the broker from `Containerfile.broker` and pins
`PODDLE_BROKER_IMAGE` at the local tag. A source build reports version `dev`,
which `resolveBrokerImage()` maps to the private `:latest` tag — so that env var
is load-bearing, not cosmetic. It's written to `~/.config/poddle/env` and sourced
from `~/.bashrc`. **Once the package is made public, drop the image build and
unset the variable.**

## Things the script handles that a hand-rolled install misses

- **`unqualified-search-registries`.** Ubuntu's podman ships none, so
  `Containerfile.broker`'s short names (`golang:1.25.13`) fail with
  `short-name … did not resolve to an alias`.
- **Rootless plumbing.** `uidmap`, `slirp4netns`, `fuse-overlayfs`, and
  `/etc/subuid` + `/etc/subgid` ranges.
- **`loginctl enable-linger`.** Load-bearing, not a nicety: without it systemd
  tears down your user slice at logout, killing the broker and every detached
  pod, and `/run/user/$UID` (podman's runtime dir) doesn't exist at boot. This is
  what makes `poddle task -d` survive a disconnect.
- **CGO_ENABLED=0**, matching `Containerfile.broker`, so no C toolchain is needed.

## What was verified

`setup.sh` ends with a smoke test that asserts the actual security properties,
not just that a pod started. All four passed on three independent fresh Ubuntu
24.04 hosts:

| Check | Result |
|---|---|
| Allow-listed host through the broker | 200 |
| Off-policy host | 403, denied by the broker |
| Proxy env stripped, raw public IP | **Network unreachable** — no route but the broker |
| Cloud metadata (IMDS `169.254.169.254`) | 403 — **even when the policy explicitly allows it** |

The IMDS result is the interesting one: the SSRF floor is enforced at dial time,
independent of policy. poddle's audit log distinguishes the two denials
precisely — `upstream not allow-listed` vs
`egress blocked: cloud-metadata/link-local` — which is what proves it isn't the
allow-list doing the work. The audit hash-chain also survived a host reboot
(it lives on the `/state` bind mount, not in the container).

## Multiple concurrent sessions

Verified on one host: four sessions up at once, each with its own handle, policy
and lock network, all egressing simultaneously; ending one leaves the others
untouched. The daemon is built for this — `pods`, `handlePod` and `podPolicy` are
mutex-guarded maps, and per-pod policy really is per-pod (the forward proxy
resolves the egress mode per request via `EgressModer`, `broker/forward.go`).

Multiple *people* is a different question and is **not** supported: the control
socket is owner-only with no authn, and one broker holds every pod's vault. The
vault is already tenant-scoped internally (`vaultKey(tenant, credID)`, with
cross-tenant reads treated as not-found) but there is exactly one tenant —
`const localTenant = "local"`, commented *"Multi-tenant is Phase 4."* So: many
sessions, one owner.

### The idle-host bug (fixed here)

Worth knowing about because it is exactly the "baremetal box that goes idle
overnight" case, and it is fixed in this branch.

When the **last** pod went down, the broker was left attached to no lock network
and silently lost egress. It stayed *healthy* — its control socket answered
fine — but every subsequent request came back `502` with
`decision=allow detail=upstream error` (the policy allowed it; the fetch failed).
It never recovered, so the next morning's first session was broken.

Two things had to change:

- `podman.EnsureBroker` reused any running broker. It now recycles a broker with
  no pod lock networks attached, and leaves an in-use one strictly alone.
- `poddled.Client.EnsureRunning` returned early whenever `/health` passed, so the
  above never ran. `/health` only proves the control socket answers, so it now
  also checks whether a pod still depends on the broker.

A `podman restart` does *not* fix it — restoring the container restores the same
stale network state. Only a fresh container does, which is what the fix does.

Confirmed: four drain-to-zero/start cycles that previously failed every time now
pass, while concurrent sessions and churn-with-survivors are unaffected.

If you see `upstream error` 502s anyway, `./poddle-doctor.sh` diagnoses it and
`--repair` recreates the broker and egress network (identities, connections and
the audit log are preserved; running pods must be recreated).

**Caveat on all of this:** testing was nested rootless podman inside a container
on podman 4.9.3 (Ubuntu 24.04's version), not real hardware. Re-run the
drain-to-zero cycle on your actual VM before trusting it.

## Operating notes

- **Single user.** Owner-only Unix socket, no authn/authz. One VM = one person.
  One broker per host holds every pod's vault — a documented, deliberate MVP
  trade-off. Don't put a team on one box.
- **Dashboard has no authentication** and binds `127.0.0.1`. Never expose the
  port; reach it over SSH:
  ```bash
  ssh -N -L 7333:127.0.0.1:7333 user@vm    # then http://127.0.0.1:7333
  poddle dashboard                          # on the VM
  ```
- **Tokens are plaintext `0600` files** under `~/.config/poddle`
  (`src/internal/connector/connection.go`). The memguard/in-memory guarantee
  covers the broker runtime, not the connection store. Use an encrypted volume.
- **Reboot / broker crash.** The broker now runs under `--restart=always`, so
  podman restarts it on crash and `podman-restart.service` brings it back at boot
  (`setup.sh` enables the **user** instance — the system one the package enables
  does not cover rootless containers). Its pod-facing listeners are also pinned to
  fixed ports (7440-7443) instead of ephemeral ones, so a restart no longer moves
  the port out from under pods that baked it into their env.

  **But a restarted broker has amnesia, by design.** Handles and pod
  registrations live in memory only — that is the same property that keeps
  credentials off disk. So pods created *before* a broker restart present a token
  the new broker has never seen, and every request they make is refused 403
  `unrecognized egress token` regardless of policy. New pods work immediately;
  stranded ones must be recreated (`poddle down <pod> && poddle up <pod>`, or
  `poddle move`). `poddle-doctor.sh` detects this case and says so, rather than
  pointing you at `--repair` (which would not help).

  Detached **pods** are deliberately not auto-restarted: they are disposable
  shells whose state lives on named volumes.
- **No backup/restore/upgrade docs exist.** Back up `~/.config/poddle`
  (identities, connections, policies) and the audit db under
  `~/.local/state/poddle` yourself.
- Pre-1.0 (`v0.1.2`); the heavy e2e suites are `workflow_dispatch`-only, not run
  per-PR.
