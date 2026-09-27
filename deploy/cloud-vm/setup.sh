#!/usr/bin/env bash
# Provision a single-user poddle host on a fresh Ubuntu 24.04 cloud VM.
#
# poddle is single-user by design (the daemon's control socket is owner-only and
# there is no authn layer), so this sets up ONE unprivileged user who owns the
# broker, the vault, and every pod. Run it as that user; it sudo's only for the
# system-level bits (packages, registry config, lingering).
#
# It is idempotent: re-run it to pick up a new commit.
#
# Why a source build: the published poddle-broker image on ghcr.io is still
# private (docs/architecture.md, "Known gaps"), so an unauthenticated
# `poddle up` cannot pull it. We build the broker image locally from
# Containerfile.broker and pin PODDLE_BROKER_IMAGE at it. Drop steps 6-8 and
# unset PODDLE_BROKER_IMAGE once that package is made public.

set -euo pipefail

GO_VERSION="${GO_VERSION:-1.25.13}"        # must satisfy go.mod's `go` directive
PODDLE_REPO="${PODDLE_REPO:-https://github.com/datadir-lab/poddle.git}"
PODDLE_SRC="${PODDLE_SRC:-$HOME/poddle-src}"
PODDLE_REF="${PODDLE_REF:-main}"
BROKER_IMAGE="${BROKER_IMAGE:-localhost/poddle-broker:local}"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
ENV_FILE="$HOME/.config/poddle/env"

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
die() { printf '\033[31merror: %s\033[0m\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------- 1. preflight
say "Preflight"
[ "$(id -u)" -ne 0 ] || die "run this as your normal (non-root) user, not root — poddle's pods are rootless"
command -v sudo >/dev/null || die "sudo is required for the system-level steps"
[ -r /etc/os-release ] || die "cannot read /etc/os-release"
. /etc/os-release
case "$ID" in
  ubuntu|debian) ;;
  *) die "this script targets Ubuntu/Debian; on $ID install podman + Go yourself and run steps 4 onward" ;;
esac
echo "user=$(id -un) uid=$(id -u) os=$PRETTY_NAME"

# ------------------------------------------------------------- 2. host packages
# uidmap (newuidmap/newgidmap), slirp4netns and fuse-overlayfs are what make
# ROOTLESS podman work; without them `podman info` fails and every `up` dies.
say "Installing host packages"
sudo DEBIAN_FRONTEND=noninteractive apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
  podman uidmap slirp4netns fuse-overlayfs \
  curl ca-certificates git jq
echo "podman $(podman --version | awk '{print $3}')"

# --------------------------------------------------- 3. container registry config
# Ubuntu's podman ships NO unqualified-search-registries, so the short image
# names in Containerfile.broker ("golang:1.25.13") fail to resolve with
# 'short-name ... did not resolve to an alias'. poddle's own default pod image is
# fully qualified, but the broker build is not.
say "Configuring container registries"
sudo mkdir -p /etc/containers/registries.conf.d
printf 'unqualified-search-registries = ["docker.io"]\n' \
  | sudo tee /etc/containers/registries.conf.d/00-docker.conf >/dev/null

# ------------------------------------------------------------ 4. rootless plumbing
# subuid/subgid: the UID range podman maps into containers. useradd grants these
# on Ubuntu, but cloud images built by other means sometimes omit them.
say "Checking rootless UID/GID mappings"
me="$(id -un)"
if ! grep -q "^${me}:" /etc/subuid; then
  echo "${me}:100000:65536" | sudo tee -a /etc/subuid >/dev/null
  echo "added subuid range"
fi
if ! grep -q "^${me}:" /etc/subgid; then
  echo "${me}:100000:65536" | sudo tee -a /etc/subgid >/dev/null
  echo "added subgid range"
fi
echo "subuid: $(grep "^${me}:" /etc/subuid)"

# LINGERING IS LOAD-BEARING, not a nicety. Without it systemd tears down your
# user slice when the SSH session ends — killing the broker container and every
# detached pod with it — and /run/user/$UID (podman's runtime dir) does not exist
# until you log in. This is what makes `poddle task -d` survive a disconnect.
say "Enabling systemd lingering (keeps pods alive after you log out)"
if sudo loginctl enable-linger "$me" 2>/dev/null; then
  echo "linger: $(loginctl show-user "$me" --property=Linger --value 2>/dev/null || echo enabled)"
  # Rootless containers are restarted at boot by the USER instance of
  # podman-restart.service, not the system one the package enables. Without this
  # the broker's --restart=always survives a crash but not a reboot.
  if systemctl --user enable podman-restart.service >/dev/null 2>&1; then
    echo "podman-restart.service: enabled (user)"
  else
    echo "  [warn] could not enable the user podman-restart.service;" >&2
    echo "         the broker will not come back automatically after a reboot." >&2
    echo "         Fix:  systemctl --user enable --now podman-restart.service" >&2
  fi
else
  # No systemd-logind (a container, or a non-systemd init). Not fatal — pods
  # still run — but say plainly what is lost, because it is the difference
  # between `poddle task -d` surviving a disconnect and being killed with it.
  LINGER_WARNING=1
  cat >&2 <<'WARN'
  [warn] could not enable lingering (no systemd-logind on this host).
         Consequences on a systemd host without it:
           - the broker and every detached pod are killed when you log out
           - /run/user/$UID does not exist at boot, so podman fails until you log in
         Fix on a real VM:  sudo loginctl enable-linger <user>
WARN
fi

# -------------------------------------------------------------------- 5. Go
say "Installing Go ${GO_VERSION}"
if [ "$(/usr/local/go/bin/go version 2>/dev/null | awk '{print $3}')" = "go${GO_VERSION}" ]; then
  echo "already present"
else
  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  curl -fsSL -o "$tmp/go.tgz" "https://go.dev/dl/go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz"
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf "$tmp/go.tgz"
fi
export PATH="/usr/local/go/bin:$PATH"
go version

# ------------------------------------------------------------------ 6. source
say "Fetching poddle source (${PODDLE_REF})"
if [ "${PODDLE_SRC_LOCAL:-0}" = "1" ]; then
  # Build whatever is already in $PODDLE_SRC — no fetch, no reset. For deploying
  # a patched or unpushed tree (rsync it up first) without clobbering it.
  [ -d "$PODDLE_SRC" ] || die "PODDLE_SRC_LOCAL=1 but $PODDLE_SRC does not exist"
  echo "using the existing tree at $PODDLE_SRC (no fetch)"
elif [ -d "$PODDLE_SRC/.git" ]; then
  git -C "$PODDLE_SRC" fetch --quiet origin "$PODDLE_REF"
  git -C "$PODDLE_SRC" checkout --quiet "$PODDLE_REF"
  git -C "$PODDLE_SRC" reset --hard --quiet "origin/${PODDLE_REF}" 2>/dev/null \
    || git -C "$PODDLE_SRC" reset --hard --quiet "$PODDLE_REF"
else
  git clone --quiet "$PODDLE_REPO" "$PODDLE_SRC"
  git -C "$PODDLE_SRC" checkout --quiet "$PODDLE_REF"
fi
cd "$PODDLE_SRC"
describe="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
echo "at $describe"

# ------------------------------------------------------------------- 7. build
# CGO_ENABLED=0 matches Containerfile.broker and avoids needing a C toolchain.
say "Building the poddle CLI"
mkdir -p "$BIN_DIR"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${describe}" \
  -o "$BIN_DIR/poddle" ./src/cli
"$BIN_DIR/poddle" version

say "Building the broker image (${BROKER_IMAGE})"
podman build --quiet -f Containerfile.broker -t "$BROKER_IMAGE" . >/dev/null
podman image exists "$BROKER_IMAGE" || die "broker image build did not produce ${BROKER_IMAGE}"
echo "built ${BROKER_IMAGE}"

# -------------------------------------------------------------- 8. environment
# PODDLE_BROKER_IMAGE must be set for EVERY poddle invocation. A source build
# reports version "dev", which resolveBrokerImage() maps to the ghcr :latest tag
# — and that package is private, so without this the first `up` fails on pull.
say "Writing environment"
mkdir -p "$(dirname "$ENV_FILE")"
cat > "$ENV_FILE" <<EOF
# Written by deploy/cloud-vm/setup.sh — sourced from your shell profile.
export PATH="\$HOME/.local/bin:/usr/local/go/bin:\$PATH"
export PODDLE_BROKER_IMAGE="${BROKER_IMAGE}"
EOF

marker="# poddle (deploy/cloud-vm/setup.sh)"
profile="$HOME/.bashrc"
if ! grep -qF "$marker" "$profile" 2>/dev/null; then
  printf '\n%s\n[ -f %s ] && . %s\n' "$marker" "$ENV_FILE" "$ENV_FILE" >> "$profile"
  echo "hooked $ENV_FILE into $profile"
fi
# shellcheck disable=SC1090
. "$ENV_FILE"

# -------------------------------------------------------------- 9. smoke test
say "Smoke test"
podman info --format 'rootless={{.Host.Security.Rootless}} driver={{.Store.GraphDriverName}}'

mkdir -p "$HOME/.config/poddle/policies"
if [ ! -e "$HOME/.config/poddle/policies/starter.toml" ]; then
  cat > "$HOME/.config/poddle/policies/starter.toml" <<'TOML'
description = "Starter policy: default-deny egress, allow nothing but example.com."
allow_upstreams = ["example.com"]
egress = "redact"
TOML
  echo "wrote a starter policy (~/.config/poddle/policies/starter.toml)"
fi

poddle up poddle-smoke --detach --image docker.io/library/alpine:latest --policy starter >/dev/null
cleanup_smoke() { poddle down poddle-smoke >/dev/null 2>&1 || true; }
trap cleanup_smoke EXIT

allowed="$(poddle run poddle-smoke "wget -T 20 -qO- http://example.com" 2>&1 || true)"
case "$allowed" in
  *"Example Domain"*)
    echo "  [ok]   allow-listed host reachable through the broker" ;;
  *502*)
    # The broker answered, so the pod->broker leg is fine; the broker itself
    # could not reach upstream. On a rootless host that is almost always DNS:
    # aardvark-dns serves the custom bridge (poddle-egress) at its gateway IP,
    # and if it is not listening there every upstream lookup fails.
    printf '%s\n' "$allowed" >&2
    cat >&2 <<'DIAG'
  [diag] The broker returned 502 — it is running, but could not resolve/reach
         the upstream. Check the egress network's DNS from the host:

           podman run --rm --network poddle-egress docker.io/library/alpine:latest \
             nslookup example.com

         "Connection refused" at the 10.89.x.1 gateway means aardvark-dns is not
         serving that bridge. Usually recovered with:

           podman rm -f poddle-broker && podman network rm -f poddle-egress
           pkill -f aardvark-dns          # let podman respawn it cleanly
           poddle up ...                  # recreates both

         Note: nested/rootless-in-container hosts often cannot bind aardvark on a
         custom bridge at all. poddle needs a real host (or VM) for its egress network.
DIAG
    die "broker could not reach the allow-listed host (502)" ;;
  *)
    die "allow-listed host did NOT come through the broker:\n$allowed" ;;
esac

denied="$(poddle run poddle-smoke "wget -T 20 -qO- http://pastebin.com" 2>&1 || true)"
case "$denied" in
  *403*) echo "  [ok]   off-policy host denied (403) by the broker" ;;
  *) die "off-policy host was NOT denied:\n$denied" ;;
esac

# The pod must have no route to the internet except the broker.
bypass="$(poddle run poddle-smoke \
  "env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY wget -T 10 -qO- http://1.1.1.1" 2>&1 || true)"
case "$bypass" in
  *"Network unreachable"*|*"bad address"*) echo "  [ok]   egress lockdown holds with the proxy stripped" ;;
  *) die "pod reached the network around the broker:\n$bypass" ;;
esac

# The SSRF floor blocks cloud instance metadata at dial time, regardless of policy.
imds="$(poddle run poddle-smoke "wget -T 10 -qO- http://169.254.169.254/latest/meta-data/" 2>&1 || true)"
case "$imds" in
  *403*) echo "  [ok]   cloud metadata (IMDS) blocked" ;;
  *) die "pod reached cloud instance metadata:\n$imds" ;;
esac

cleanup_smoke
trap - EXIT

say "Done"
if [ "${LINGER_WARNING:-0}" = "1" ]; then
  printf '\033[33m[warn] lingering is NOT enabled — see the warning above before relying on detached pods.\033[0m\n'
fi
cat <<EOF
poddle is installed and verified on this host.

  binary        $BIN_DIR/poddle
  source        $PODDLE_SRC
  broker image  $BROKER_IMAGE
  env           $ENV_FILE  (sourced from ~/.bashrc)

Open a NEW shell (or: . $ENV_FILE), then:

  poddle identity add work --provider anthropic     # paste the key on stdin
  echo "\$GITHUB_PAT" | poddle connect add gh --connector github
  poddle up                                         # or: poddle task "..." --detach

Audit dashboard — it binds 127.0.0.1 with no authentication, so never expose the
port. Reach it over an SSH tunnel from your laptop:

  ssh -N -L 7333:127.0.0.1:7333 $(id -un)@<this-host>    # then open http://127.0.0.1:7333
  # on the VM:
  poddle dashboard
EOF
