#!/usr/bin/env bash
# poddle-doctor — diagnose (and recover from) a broker that can no longer reach
# upstreams, i.e. every pod request returns "502 Bad Gateway" while the host
# itself has working internet.
#
# What the symptom looks like in poddle's own audit log:
#
#     kind=request upstream=example.com status=502 decision=allow detail=upstream error
#
# `decision=allow` is the important part: the POLICY permitted the request, so
# this is not a governance denial (those read `decision=deny`). The broker
# accepted it and then failed to fetch it.
#
# Known trigger (observed, cause not fully isolated): after some pod up/down
# churn the broker stops being able to resolve/reach upstreams on its custom
# bridge network (poddle-egress) and does not recover on its own. A freshly
# created broker + egress network always works for at least the first pod, which
# is why --repair recreates both.
#
#   poddle-doctor.sh           # diagnose only, exit 1 if unhealthy
#   poddle-doctor.sh --repair  # diagnose, then recreate the broker + egress network
#
# --repair is disruptive: running pods lose their broker route and must be
# recreated (`poddle down <pod>`, then `poddle up`). Identities, connections and
# the audit log are NOT touched — they live on disk, not in the broker container.

set -euo pipefail

NET="${PODDLE_EGRESS_NET:-poddle-egress}"
BROKER="${PODDLE_BROKER_NAME:-poddle-broker}"
PROBE_IMAGE="${PODDLE_PROBE_IMAGE:-docker.io/library/alpine:latest}"
REPAIR=0
[ "${1:-}" = "--repair" ] && REPAIR=1

: "${XDG_RUNTIME_DIR:=/run/user/$(id -u)}"
export XDG_RUNTIME_DIR
AARDVARK_DIR="$XDG_RUNTIME_DIR/containers/networks/aardvark-dns"
SOCK="${PODDLE_SOCKET:-$XDG_RUNTIME_DIR/poddle/poddled.sock}"

ok()   { printf '  \033[32m[ok]\033[0m   %s\n' "$*"; }
bad()  { printf '  \033[31m[bad]\033[0m  %s\n' "$*"; }
info() { printf '  \033[2m[--]\033[0m   %s\n' "$*"; }

problems=0
stranded=0

echo "poddle-doctor: checking the broker's egress path"

# 1. The host itself. If this fails nothing downstream is meaningful.
if getent hosts example.com >/dev/null 2>&1; then
  ok "host DNS resolves"
else
  bad "the HOST cannot resolve example.com — fix host networking first"
  exit 1
fi

# 2. The most direct evidence available: what the broker recorded. A recent
#    "upstream error" at decision=allow is the exact fingerprint of this fault.
if [ -S "$SOCK" ] && command -v curl >/dev/null; then
  recent="$(curl -s --unix-socket "$SOCK" http://unix/audit 2>/dev/null || true)"
  if printf '%s' "$recent" | grep -q 'upstream error'; then
    bad "the audit log contains \"upstream error\" — the broker could not fetch an ALLOWED request"
    problems=1
  elif [ -n "$recent" ]; then
    ok "no \"upstream error\" in the audit log"
  fi
  # A DIFFERENT fault with a similar surface: the broker restarted (crash,
  # reboot, podman-restart) and came back with an empty vault. Handles and pod
  # registrations are in-memory ONLY — deliberately, since credentials are never
  # written to disk — so pods created before the restart present a token the new
  # broker has never seen. Everything they request is refused 403 regardless of
  # policy. The fix is to recreate those pods, NOT to repair the broker.
  if printf '%s' "$recent" | grep -q 'unrecognized egress token'; then
    bad "the audit log contains \"unrecognized egress token\" — the broker restarted and lost its handles"
    info "pods created BEFORE that restart are stranded and must be recreated:"
    info "    poddle down <pod> && poddle up <pod> ...   (or: poddle move <pod>)"
    info "this is by design — the vault is in memory, never on disk — not a broker fault"
    stranded=1
  fi
else
  info "poddled socket not present — skipping the audit check (daemon not running yet)"
fi

# 3. Can anything on the egress bridge resolve? This is the layer that breaks.
if podman network exists "$NET" 2>/dev/null; then
  ok "network $NET exists"
  probe="$(timeout 120 podman run --rm --network "$NET" "$PROBE_IMAGE" \
             nslookup example.com 2>&1 || true)"
  if printf '%s' "$probe" | grep -qE '^Address: [0-9]'; then
    ok "DNS resolves on $NET"
  else
    bad "DNS does NOT resolve on $NET — the broker cannot look up any upstream"
    printf '%s\n' "$probe" | sed 's/^/         /' | head -3
    problems=1
  fi
else
  info "network $NET does not exist yet (created on the next \`poddle up\`)"
fi

# 4. aardvark-dns serves that bridge. Report its state; a dead pidfile or several
#    competing instances are both worth knowing, though neither is always the cause.
if [ -f "$AARDVARK_DIR/aardvark.pid" ]; then
  livepid="$(cat "$AARDVARK_DIR/aardvark.pid" 2>/dev/null || echo)"
  # pgrep -c prints "0" AND exits non-zero when there is no match, so a
  # `|| echo 0` fallback would emit "0\n0" and break the arithmetic test below.
  running="$(pgrep -c -x aardvark-dns 2>/dev/null)" || running=0
  if [ -n "$livepid" ] && kill -0 "$livepid" 2>/dev/null; then
    ok "aardvark-dns running (pid $livepid)"
  else
    bad "aardvark-dns pidfile points at ${livepid:-?}, which is not running"
    problems=1
  fi
  [ "$running" -gt 1 ] && { bad "$running aardvark-dns instances running — stale ones contend for port 53"; problems=1; }
else
  info "no aardvark pidfile yet"
fi

if [ "$problems" -eq 0 ] && [ "$stranded" -eq 0 ]; then
  echo; echo "No problems found."
  exit 0
fi

# Stranded pods are NOT repaired by recreating the broker — the broker is fine,
# it simply never saw those pods. Recreating it again would strand even more.
if [ "$problems" -eq 0 ]; then
  cat <<EOF

The broker itself is healthy. Pods created before its last restart are stranded
and must be recreated:

    poddle ls
    poddle down <pod> && poddle up <pod> --detach --policy <policy>

Do NOT run --repair for this — it recreates the broker, which is not the problem.
EOF
  exit 1
fi

echo
if [ "$REPAIR" -ne 1 ]; then
  cat <<EOF
Unhealthy. Recreate the broker and its egress network with:

    $(basename "$0") --repair

Running pods will need to be recreated afterwards. Identities, connections and
the audit log are preserved.
EOF
  exit 1
fi

echo "Repairing…"
# The broker holds the bridge, so it goes first; then the network; then the
# aardvark instance that serves it (podman respawns it on the next create).
podman rm -f "$BROKER" >/dev/null 2>&1 && echo "  removed $BROKER" || true
podman network rm -f "$NET" >/dev/null 2>&1 && echo "  removed network $NET" || true
# Kill by EXACT name. Never `pkill -f aardvark-dns`: the -f pattern also matches
# the shell running this script, which then terminates itself mid-repair.
pkill -x aardvark-dns 2>/dev/null && echo "  stopped aardvark-dns" || true
rm -f "$AARDVARK_DIR/$NET" 2>/dev/null || true
sleep 2

cat <<EOF

Repaired. Recreate a pod to rebuild the network and broker:

    poddle up <name> --detach --policy <policy>

Then re-check with:  $(basename "$0")
If 502s come back after a few pod cycles, that is the unresolved churn issue —
please capture \`$(basename "$0")\` output and the audit log for a bug report.
EOF
