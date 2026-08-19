#!/bin/sh
# Build the @poddle/cli npm packages for a released version.
#
#   build.sh <version> <out-dir>
#
# Downloads the six release archives, extracts the poddle binary from each, and
# runs generate.mjs to emit <out-dir>/{cli,cli-<os>-<cpu>...} ready to publish.
# Used by both the publish-cli workflow and the one-time bootstrap.
set -eu

VER="${1#v}"
OUT="$2"
REPO="${PODDLE_REPO:-datadir-lab/poddle}"
base="https://github.com/$REPO/releases/download/v$VER"
here="$(CDPATH= cd "$(dirname "$0")" && pwd)"
staging="$(mktemp -d)"

for plat in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
	mkdir -p "$staging/$plat"
	curl -fsSL "$base/poddle_${VER}_${plat}.tar.gz" | tar -xz -C "$staging/$plat" poddle
done
for plat in windows_amd64 windows_arm64; do
	mkdir -p "$staging/$plat"
	tmp="$(mktemp -d)"
	curl -fsSL "$base/poddle_${VER}_${plat}.zip" -o "$tmp/p.zip"
	unzip -oq "$tmp/p.zip" poddle.exe -d "$staging/$plat"
done

node "$here/generate.mjs" --version "$VER" --staging "$staging" --out "$OUT"
