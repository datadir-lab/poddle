#!/bin/sh
# poddle installer.
#
#   curl -sSf https://raw.githubusercontent.com/datadir-lab/poddle/HEAD/install.sh | sh
#
# The download is verified: the archive's sha256 against checksums.txt, and (if
# cosign is installed) the cosign keyless signature over checksums.txt. Install
# cosign for full supply-chain verification.
#
# Env overrides:
#   PODDLE_VERSION       tag to install (default: latest release)
#   PODDLE_INSTALL_DIR   install dir (default: /usr/local/bin, else ~/.local/bin)
#   PODDLE_SKIP_VERIFY   set to any value to skip checksum/signature verification
#
# poddle runs pods with podman; install that separately.
set -eu

REPO="datadir-lab/poddle"
BIN="poddle"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
err() {
	printf '\033[1;31merror:\033[0m %s\n' "$1" >&2
	exit 1
}

command -v uname >/dev/null 2>&1 || err "uname not found"
command -v tar >/dev/null 2>&1 || err "tar not found"

# Downloader (curl or wget).
if command -v curl >/dev/null 2>&1; then
	dl() { curl -fsSL "$1"; }
	dlo() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	dl() { wget -qO- "$1"; }
	dlo() { wget -qO "$2" "$1"; }
else
	err "need curl or wget"
fi

# OS + arch -> goreleaser archive naming.
os=$(uname -s)
arch=$(uname -m)
case "$os" in
Linux) goos=linux ;;
Darwin) goos=darwin ;;
*) err "unsupported OS: $os (try: go install github.com/$REPO/src@latest)" ;;
esac
case "$arch" in
x86_64 | amd64) goarch=amd64 ;;
arm64 | aarch64) goarch=arm64 ;;
*) err "unsupported arch: $arch" ;;
esac

# Resolve version.
ver="${PODDLE_VERSION:-}"
if [ -z "$ver" ]; then
	ver=$(dl "https://api.github.com/repos/$REPO/releases/latest" |
		grep '"tag_name"' | head -1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
	[ -n "$ver" ] || err "could not resolve latest version; set PODDLE_VERSION"
fi
num=${ver#v}
info "Installing poddle $ver ($goos/$goarch)"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
archive="${BIN}_${num}_${goos}_${goarch}.tar.gz"
base="https://github.com/$REPO/releases/download/$ver"
dlo "$base/$archive" "$tmp/$archive" || err "download failed: $archive"

# --- Verify the download --------------------------------------------------
# Two layers: (1) integrity - the archive's sha256 must match checksums.txt;
# (2) authenticity - checksums.txt carries a cosign keyless signature from the
# release workflow, so a matching checksum proves the archive is genuinely ours.
# Verification runs when the tools are present; set PODDLE_SKIP_VERIFY=1 to skip.
if command -v sha256sum >/dev/null 2>&1; then
	sumcmd="sha256sum -c"
elif command -v shasum >/dev/null 2>&1; then
	sumcmd="shasum -a 256 -c"
else
	sumcmd=""
fi
if [ -n "${PODDLE_SKIP_VERIFY:-}" ]; then
	info "PODDLE_SKIP_VERIFY set; skipping checksum and signature verification"
elif dlo "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
	if [ -n "$sumcmd" ]; then
		grep " $archive$" "$tmp/checksums.txt" >"$tmp/sum" 2>/dev/null || err "no checksum listed for $archive"
		if (cd "$tmp" && $sumcmd sum >/dev/null 2>&1); then
			info "checksum verified"
		else
			err "checksum verification failed"
		fi
	else
		info "no sha256 tool; skipping checksum verification"
	fi

	# Authenticity: verify the cosign keyless signature over checksums.txt.
	if command -v cosign >/dev/null 2>&1; then
		if dlo "$base/checksums.txt.sig" "$tmp/checksums.txt.sig" 2>/dev/null &&
			dlo "$base/checksums.txt.pem" "$tmp/checksums.txt.pem" 2>/dev/null; then
			if cosign verify-blob \
				--certificate "$tmp/checksums.txt.pem" \
				--signature "$tmp/checksums.txt.sig" \
				--certificate-identity-regexp "^https://github.com/$REPO/\.github/workflows/release\.yml@refs/tags/.*\$" \
				--certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
				"$tmp/checksums.txt" >/dev/null 2>&1; then
				info "signature verified (cosign)"
			else
				err "cosign signature verification failed"
			fi
		else
			info "signature files unavailable; skipping cosign verification"
		fi
	else
		info "cosign not found; skipping signature check (install cosign for full verification)"
	fi
else
	info "checksums.txt unavailable; skipping verification"
fi

tar -xzf "$tmp/$archive" -C "$tmp"
[ -f "$tmp/$BIN" ] || err "binary '$BIN' not found in archive"

# Install dir.
dir="${PODDLE_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		dir=/usr/local/bin
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir"
if [ -w "$dir" ]; then
	install -m 0755 "$tmp/$BIN" "$dir/$BIN"
else
	info "writing to $dir needs sudo"
	sudo install -m 0755 "$tmp/$BIN" "$dir/$BIN"
fi

info "poddle installed to $dir/$BIN"
case ":$PATH:" in
*":$dir:"*) ;;
*) info "add $dir to your PATH to run 'poddle'" ;;
esac
"$dir/$BIN" version 2>/dev/null || true
