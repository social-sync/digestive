#!/bin/sh
# grimnir installer.
#
#   curl -sSfL https://raw.githubusercontent.com/social-sync/grimnir/main/install.sh | sh
#
# Downloads the release asset matching your OS/arch from GitHub, verifies its
# SHA-256 checksum, and installs the `grimnir` binary.
#
# Environment overrides:
#   GRIMNIR_VERSION   tag to install (default: latest release, e.g. v1.2.3)
#   GRIMNIR_BIN_DIR   install directory (default: /usr/local/bin, or ~/.local/bin
#                     if that is not writable)
set -eu

REPO="social-sync/grimnir"
BINARY="grimnir"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1" >&2; }
warn() { printf '\033[1;33mwarn:\033[0m %s\n' "$1" >&2; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$1" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"; }

need uname
need tar
need mktemp
# One of curl/wget is required for downloads.
if command -v curl >/dev/null 2>&1; then
  DL="curl -sSfL"
  DL_O="curl -sSfL -o"
elif command -v wget >/dev/null 2>&1; then
  DL="wget -qO-"
  DL_O="wget -qO"
else
  err "need either curl or wget installed"
fi

# --- detect platform ---------------------------------------------------------
os=$(uname -s)
case "$os" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *) err "unsupported OS: $os (use the Windows zip from the releases page)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64)  ARCH=amd64 ;;
  arm64 | aarch64) ARCH=arm64 ;;
  *) err "unsupported architecture: $arch" ;;
esac

# --- resolve version ---------------------------------------------------------
VERSION="${GRIMNIR_VERSION:-}"
if [ -z "$VERSION" ]; then
  info "Resolving latest release..."
  # Ask GitHub for the latest release tag.
  VERSION=$($DL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name":' | head -n1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
  [ -n "$VERSION" ] || err "could not determine latest version; set GRIMNIR_VERSION"
fi
# Version number without the leading "v" (matches the archive file name).
NUM=${VERSION#v}

ASSET="${BINARY}_${NUM}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/${REPO}/releases/download/${VERSION}"

# --- download & verify -------------------------------------------------------
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

info "Downloading ${ASSET} (${VERSION})..."
$DL_O "$TMP/$ASSET" "$BASE/$ASSET" || err "download failed: $BASE/$ASSET"

info "Verifying checksum..."
if $DL_O "$TMP/checksums.txt" "$BASE/checksums.txt" 2>/dev/null; then
  expected=$(grep " ${ASSET}\$" "$TMP/checksums.txt" | awk '{print $1}')
  if [ -z "$expected" ]; then
    warn "no checksum entry for ${ASSET}; skipping verification"
  else
    if command -v sha256sum >/dev/null 2>&1; then
      actual=$(sha256sum "$TMP/$ASSET" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      actual=$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')
    else
      warn "no sha256 tool found; skipping verification"
      actual="$expected"
    fi
    [ "$actual" = "$expected" ] || err "checksum mismatch for ${ASSET}"
  fi
else
  warn "checksums.txt not found; skipping verification"
fi

# --- install -----------------------------------------------------------------
info "Extracting..."
tar -xzf "$TMP/$ASSET" -C "$TMP" "$BINARY" || err "failed to extract $BINARY"
chmod +x "$TMP/$BINARY"

BIN_DIR="${GRIMNIR_BIN_DIR:-/usr/local/bin}"
if [ ! -d "$BIN_DIR" ] || [ ! -w "$BIN_DIR" ]; then
  if [ -n "${GRIMNIR_BIN_DIR:-}" ]; then
    err "install dir not writable: $BIN_DIR"
  fi
  BIN_DIR="$HOME/.local/bin"
  mkdir -p "$BIN_DIR"
  warn "/usr/local/bin not writable; installing to $BIN_DIR"
fi

mv "$TMP/$BINARY" "$BIN_DIR/$BINARY"
info "Installed $BINARY to $BIN_DIR/$BINARY"

case ":$PATH:" in
  *":$BIN_DIR:"*) : ;;
  *) warn "$BIN_DIR is not on your PATH; add it to use '$BINARY' directly" ;;
esac

"$BIN_DIR/$BINARY" --version 2>/dev/null || true
