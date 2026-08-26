<div align="center">

<br>

<h1>
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/logo-wordmark-dark.png">
    <img src=".github/assets/logo-wordmark.png" alt="poddle" width="440">
  </picture>
</h1>

**Self-hostable, secret-safe dev sandboxes for coding agents.**

[![CI](https://github.com/datadir-lab/poddle/actions/workflows/ci.yml/badge.svg)](https://github.com/datadir-lab/poddle/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/datadir-lab/poddle?sort=semver)](https://github.com/datadir-lab/poddle/releases)
[![codecov](https://codecov.io/gh/datadir-lab/poddle/branch/main/graph/badge.svg)](https://codecov.io/gh/datadir-lab/poddle)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/datadir-lab/poddle/badge)](https://securityscorecards.dev/viewer/?uri=github.com/datadir-lab/poddle)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/14171/badge)](https://www.bestpractices.dev/projects/14171)
[![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](./LICENSE)

[**Website**](https://poddle.dev) · [**Quickstart**](#quickstart) · [**How it works**](#how-it-works) · [**Docs**](https://poddle.dev/docs)

</div>

<p align="center">
  <img src=".github/assets/dashboard-audit.png" width="860"
       alt="poddle dashboard audit log: every brokered request with an allow, redact, deny, or block decision" />
</p>

<p align="center"><i>Every request a pod makes is brokered, attributed, and policy-checked &mdash; the real secret never leaves your host.</i></p>

---

`poddle up` spins an isolated, reproducible pod on your own infra, wired to your
stack (git host, CI, registries, databases), with **no real secret inside the
pod**. The agent gets a revocable *handle*; a broker on your host holds the real
credential and swaps it in on the wire. Revoke a pod and its access dies at once.

<p align="center">
  <img src=".github/assets/demo.svg" width="820"
       alt="Terminal demo in three scenes: poddle up --detach and poddle attach open a pod, where env shows Claude Code is authed with a poddle handle rather than your key; a fullscreen Claude Code session then edits a file, runs the tests, and git-pushes, while poddle blocks an off-policy fetch to pastebin.com; finally poddled audit lists every brokered request with its decision - allow, redact (a secret scrubbed), and deny - and an intact tamper-evident chain, so the real secrets never leave the host." />
</p>

```bash
poddle up                       # interactive secretless sandbox
poddle task "add tests for X"   # run an agent headless, to completion
poddle task "big refactor" -d   # or in the background (poddle logs / down)
```

## Why

Giving a coding agent real access means handing it your API keys, git tokens, and
DB passwords. poddle doesn't. The pod holds only opaque handles; the broker holds
the credentials, injects them per request, logs attribution, and can redact
secrets from outbound traffic. Everything runs on **your** infra: local podman
today, a remote SSH host with one flag.

## Install

```bash
# Any platform (downloads the signed release binary):
curl -sSf https://get.poddle.dev | sh

# npm (any platform):
npm i -g @poddle/cli

# Homebrew (macOS or Linuxbrew):
brew tap datadir-lab/tap
brew trust datadir-lab/tap   # Homebrew 6.0+: trust the third-party tap once
brew install poddle

# Scoop (Windows):
scoop bucket add poddle https://github.com/datadir-lab/scoop-bucket && scoop install poddle
```

Linux `.deb` / `.rpm` / `.apk` packages are attached to each [release](https://github.com/datadir-lab/poddle/releases). From source: `git clone` the repo and `go build -o poddle ./src/cli` (or `task build`).

Requires [podman](https://podman.io) on the host that runs the pods.

## Quickstart

The fastest path &mdash; sensible defaults, no config:

```bash
poddle up                                             # secretless sandbox, attached
poddle task "fix the failing parser test and open a PR"
```

<details>
<summary><b>Full setup</b> &mdash; bring your own identities, connectors, and a <code>.poddle.toml</code></summary>

```bash
# 1. Add a login for your agent (token via stdin, never argv). Re-auth is
#    prompted when stale; the real token stays on your machine.
poddle identity add work --provider anthropic
# or: poddle identity add work --provider openai  # with OPENAI_API_KEY
# or: poddle identity add work --provider google  # with GEMINI_API_KEY

# 2. Broker a service or two (git host, CI, registry, database).
echo "$GITHUB_PAT" | poddle connect add gh  --connector github
echo "$PG_PASS"    | poddle connect add db  --connector postgres \
                       --url postgres://10.0.0.9:5432/shop --user app

# 3. Describe the pod (optional; sensible defaults otherwise).
cat > .poddle.toml <<'TOML'
image      = "docker.io/library/node:22"
identity   = "work"
connectors = ["gh", "db"]
TOML

# 4. Go.
poddle up                       # attach an interactive session
poddle task "fix the failing parser test and open a PR"
```

</details>

Inside the pod, `git`, `psql`, `npm`, and the rest just work, each authenticated
through the broker with a handle. The real credentials are never present.

## How it works

```
   pod  ──handle──▶  broker (your host)  ──real credential──▶  service
        never sees          holds creds in memory,            git / CI /
        the secret          injects on the wire,              registry / DB /
                            redacts egress, revocable         LLM API
```

- **HTTP services** (LLM APIs, git over HTTP, CI, npm/pypi): a reverse-proxy
  gateway swaps the handle for the real header on each request.
- **Databases** (Postgres, Redis): auth binds to the connection, so the broker
  terminates it. It authenticates the pod with its handle, does the real
  handshake (Postgres SCRAM-SHA-256, Redis `AUTH`) with the real password, then
  splices the sockets. The pod never learns the password.

The broker runs as a persistent daemon (`poddled`, auto-started), so pods keep
working after the client exits and can be reattached.

For the full picture — the containerized broker, egress lockdown, the
control/data-plane split, governance, and TLS interception — with diagrams, see
[`docs/architecture.md`](docs/architecture.md).

## What's brokered

| Kind | Connectors |
|---|---|
| **Git** | github, gitlab, forgejo, gitea, bitbucket |
| **CI** | woodpecker, drone, argocd, jenkins |
| **Registries** | npm, pypi, docker |
| **Databases** | postgres, redis |
| **LLM** | anthropic (claude-code), openai (codex, aider, pi, opencode), google (gemini) |

New HTTP services are a few lines of declarative TOML, no code.

## Customizing your agent

Each pod-side agent reads its own native config. Drop yours at
`~/.config/poddle/harness/<harness>/` — e.g. `codex/config.toml` with a
`[mcp_servers]` block, `opencode/opencode.json` with an `mcp` block, or
`pi/mcp.json` (plus `extensions/`) — and poddle seeds it into the pod on `up`
and persists it across `move`, without ever overwriting it. Poddle's broker
wiring rides a separate side channel (codex via `-c` overrides, opencode via a
dedicated config layer), so your full native config — settings, plugins, MCP
servers — stays yours. claude-code is a little different: poddle merges its
onboarding flag into `~/.claude.json` instead of overwriting it, so your
user-scope `mcpServers` survive a `poddle task`; gemini works the same way —
poddle merges its auth-path selection into `~/.gemini/settings.json`, so your
other settings and `mcpServers` stay intact. aider needs no host seed at
all — its config is a project file (`.aider.conf.yml`) checked into the repo,
and `/workspace` already persists.

A *local* stdio MCP server runs in the pod; one that fetches at launch needs its
registry allowed (codex and opencode permit npm by default). A *remote* MCP
server can be brokered too: `echo $MCP_TOKEN | poddle connect add search
--connector mcp --url https://mcp.example.com/mcp` seals the token in the
broker vault; add the connector to a pod and poddle registers the server with
the agent through a revocable handle — the real token never enters the pod,
and `poddle down` revokes it. Wired for **codex, claude-code, opencode, pi, and gemini**
today (pi via the third-party `pi-mcp-adapter` extension; aider has no MCP client, so
there's nothing to wire); bearer-token / API-key servers work as above, and OAuth 2.1
servers are supported too — omit `--token` and poddle probes the URL, runs browser
consent on the host (auto-discovery + Dynamic Client Registration, or
`--client-id`/`--client-secret` when the server has no DCR), and the broker refreshes
the access token automatically — tokens stay in the broker, never the pod
(`poddle connect reauth <name>` recovers a revoked/expired grant). See
[Configuration](https://poddle.dev/docs/configuration#brokered-mcp) for the full
walkthrough, including the egress note for explicit policies and the v1
refresh-token-rotation limitation.

## Secret-safety

- **`block_paths`**: mounts that would expose host secrets (`~/.ssh`, `~/.aws`,
  poddle's own token store) are refused before the pod is created.
- **credential scan**: bind mounts are scanned for `.env`/keys/`.npmrc`; warn by
  default, or `secret_scan = "block"`.
- **egress redaction**: the broker scrubs its managed secrets plus
  high-confidence patterns (private keys, `AKIA...`, `ghp_...`) from outbound
  bodies (`egress = redact | block | off`).

<details>
<summary><b>All commands</b></summary>

```
poddle up [name]            create a secretless pod and attach (--detach to leave it)
poddle task "<prompt>"      run a coding agent headless to completion (--detach for background)
poddle logs <pod>           show a detached task's output (--follow to stream)
poddle attach <pod>         reconnect to a running pod
poddle run <pod> <cmd>...   run a command in a running pod
poddle resize <pod> <size>  change a running pod's CPU live (no restart)
poddle move <pod> [--size]  re-home the session onto a fresh, re-sized shell
poddle ls                   list pods
poddle stats                live CPU/memory for running pods
poddle down <pod>           revoke the pod's handles and remove it
poddle identity add|ls|rm   manage agent logins
poddle connect add|ls|rm    manage brokered service connections
poddle daemon status        is poddled running? what is it serving?
poddle version
```

</details>

<details>
<summary><b>Sizing, moving &amp; self-hosting</b></summary>

### Sizing

`size` (weak/strong) is a **CPU ceiling, not a reservation**: idle pods float to
~0 and burst to the cap for free, so oversubscription is fine. Resize a running
pod's CPU live with `poddle resize <pod> strong` (no restart), or let a task
burst its own CPU with `before_task = "strong"` / `after_task = "weak"` in a
template (CPU only).

Memory is different. It's incompressible, so you can't safely shrink it live
without OOMing the pod. The answer to "needs more RAM" is `poddle move` (below),
not resize. Live memory resize is grow-only and needs cgroup delegation (a
rootful or systemd-delegated host).

### Moving a session

Pods are disposable compute shells; your workspace and agent state live on named
volumes. `poddle move <pod> --size strong` (or `--image`) re-homes the session
onto a fresh, right-sized shell: same workspace, same conversation, fresh
handles, in seconds (the volumes aren't copied). It's how you get more RAM, swap
the base image, or recover from a dead pod. `poddle down` removes the shell and
its volumes.

### Self-host and remote

`PODDLE_HOST=ssh://user@host` runs pods on a remote machine over the same code
path. The broker binds an owner-only Unix control socket; credentials live in
memory (memguard), never on disk.

</details>

<details>
<summary><b>Development</b></summary>

```
src/cli/              CLI entry + one vertical slice per command
src/internal/         private kernel: broker, poddled, l4, connector, podman
tests/e2e/            end-to-end tests driving the built binary against podman
.github/workflows/    CI on GitHub Actions: ci, e2e, codeql, scorecard, release
Taskfile.yml          all dev commands
```

```bash
task build    # build the binary
task test     # unit tests (co-located in src/)
task arch     # architecture / boundary tests
task ci       # what CI runs (fmt + vet + test + arch + build)
task e2e-*    # end-to-end suites (need podman): e2e-up, e2e-connector, e2e-l4, e2e-task
```

Module `github.com/datadir-lab/poddle`; imports carry the `src/` segment.

</details>

## License

poddle's core (this repository) is open source under the
**[GNU AGPL-3.0](./LICENSE)**. The hosted **poddle cloud** and **poddle
enterprise** editions are commercial and live in a separate, private repository.

- **[LICENSING.md](./LICENSING.md)**: what's licensed how, and how the
  proprietary editions coexist with an AGPL core.
- **[CONTRIBUTING.md](./CONTRIBUTING.md)**: how to contribute (DCO sign-off).
- **[docs/design/open-core.md](./docs/design/open-core.md)**: the engineering
  boundary between core and cloud.

A commercial license is available if AGPL-3.0 doesn't fit:
[hello@datadir.co](mailto:hello@datadir.co).
