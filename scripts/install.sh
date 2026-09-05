#!/usr/bin/env bash
# Install xdlc CLI from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh | sh
#
# Env:
#   XDLC_VERSION      release tag (e.g. v0.0.1-beta.1). Default: newest release (incl. prereleases).
#   XDLC_INSTALL_DIR  install directory. Default: ~/.local/bin
#   XDLC_REPO         owner/name. Default: xdlc-labs/xdlc-agent
set -euo pipefail

REPO="${XDLC_REPO:-xdlc-labs/xdlc-agent}"
INSTALL_DIR="${XDLC_INSTALL_DIR:-${HOME}/.local/bin}"

die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "need $1"; }

need curl
need tar
need mktemp
need uname

os="$(uname -s)"
case "$os" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS: $os (linux/darwin only)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported arch: $arch (amd64/arm64 only)" ;;
esac

resolve_tag() {
  if [ -n "${XDLC_VERSION:-}" ]; then
    printf '%s\n' "$XDLC_VERSION"
    return
  fi
  # /releases/latest skips prereleases; list endpoint returns newest first.
  # Indentation in GitHub JSON varies; do not anchor on leading spaces.
  tag="$(
    curl -fsSL "https://api.github.com/repos/${REPO}/releases" \
      | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      | head -n 1
  )"
  [ -n "$tag" ] || die "could not resolve latest release tag for ${REPO} (set XDLC_VERSION=v… to pin)"
  printf '%s\n' "$tag"
}

tag="$(resolve_tag)"
case "$tag" in
  v*) ver="${tag#v}" ;;
  *)  ver="$tag"; tag="v${tag}" ;;
esac

asset="xdlc-agent_${ver}_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/download/${tag}"
url="${base}/${asset}"
sum_url="${base}/checksums.txt"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

printf 'install.sh: downloading %s\n' "$url"
curl -fsSL "$url" -o "${tmpdir}/${asset}"
curl -fsSL "$sum_url" -o "${tmpdir}/checksums.txt"

(
  cd "$tmpdir"
  if command -v sha256sum >/dev/null 2>&1; then
    grep "  ${asset}\$" checksums.txt | sha256sum -c -
  elif command -v shasum >/dev/null 2>&1; then
    expected="$(grep "  ${asset}\$" checksums.txt | awk '{print $1}')"
    [ -n "$expected" ] || die "checksum missing for ${asset}"
    actual="$(shasum -a 256 "$asset" | awk '{print $1}')"
    [ "$expected" = "$actual" ] || die "checksum mismatch for ${asset}"
  else
    die "need sha256sum or shasum to verify download"
  fi
  tar -xzf "$asset"
)

# Prefer renamed binary; fall back to older release asset name.
bin=""
for candidate in xdlc xdlc-agent; do
  if [ -f "${tmpdir}/${candidate}" ] && [ -x "${tmpdir}/${candidate}" ]; then
    bin="${tmpdir}/${candidate}"
    break
  fi
  # Some archives nest the binary; scan one level.
  found="$(find "$tmpdir" -maxdepth 2 -type f -name "$candidate" 2>/dev/null | head -n 1 || true)"
  if [ -n "$found" ]; then
    bin="$found"
    break
  fi
done
[ -n "$bin" ] || die "archive has no xdlc / xdlc-agent binary"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$bin" "${INSTALL_DIR}/xdlc"

printf 'install.sh: installed %s (%s)\n' "${INSTALL_DIR}/xdlc" "$tag"
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    printf 'install.sh: add to PATH:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" >&2
    ;;
esac
"${INSTALL_DIR}/xdlc" version 2>/dev/null || true
