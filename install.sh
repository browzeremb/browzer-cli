#!/usr/bin/env sh
# Browzer CLI installer.
#
# Detects OS/arch, downloads the matching tarball from the latest
# GitHub release, extracts the binary into ~/.local/bin (or
# /usr/local/bin if writable as root), and verifies the checksum.
#
# Usage:
#   curl -fsSL https://browzeremb.com/install.sh | sh
#   curl -fsSL https://browzeremb.com/install.sh | sh -s -- --version v0.1.0
#
# Env overrides:
#   BROWZER_VERSION   pin a specific tag (e.g. v0.1.0; default: latest)
#   BROWZER_PREFIX    install dir (default: $HOME/.local/bin)
#

set -eu

REPO="browzeremb/browzer-cli"
BIN_NAME="browzer"
VERSION="${BROWZER_VERSION:-latest}"
PREFIX="${BROWZER_PREFIX:-$HOME/.local/bin}"

err() { printf "error: %s\n" "$*" >&2; exit 1; }
info() { printf "%s\n" "$*"; }

need() { command -v "$1" >/dev/null 2>&1 || err "missing dependency: $1"; }
need uname
need tar
need mkdir
if command -v curl >/dev/null 2>&1; then
  DL="curl -fsSL"
elif command -v wget >/dev/null 2>&1; then
  DL="wget -qO-"
else
  err "need curl or wget"
fi

# --- Detect OS/arch ----------------------------------------------------------
OS_RAW=$(uname -s)
ARCH_RAW=$(uname -m)

case "$OS_RAW" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *) err "unsupported OS: $OS_RAW" ;;
esac

case "$ARCH_RAW" in
  x86_64|amd64)  ARCH=x86_64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) err "unsupported architecture: $ARCH_RAW" ;;
esac

# --- Resolve version ---------------------------------------------------------
if [ "$VERSION" = "latest" ]; then
  info "→ Resolving latest release..."
  TAG=$($DL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' \
    | head -n1 \
    | sed -E 's/.*"tag_name": "([^"]+)".*/\1/')
  [ -n "$TAG" ] || err "could not resolve latest tag from GitHub API"
else
  TAG="$VERSION"
fi
VER="${TAG#v}"

ARCHIVE="browzer-cli_${VER}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$TAG/$ARCHIVE"
CHECKSUMS_URL="https://github.com/$REPO/releases/download/$TAG/checksums.txt"

# --- Download + verify -------------------------------------------------------
TMP=$(mktemp -d 2>/dev/null || mktemp -d -t browzer-install)
trap 'rm -rf "$TMP"' EXIT INT TERM

info "→ Downloading $ARCHIVE"
$DL "$URL" > "$TMP/$ARCHIVE" || err "download failed: $URL"
$DL "$CHECKSUMS_URL" > "$TMP/checksums.txt" || err "checksum download failed"

if command -v sha256sum >/dev/null 2>&1; then
  EXPECTED=$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | awk '{print $1}')
  ACTUAL=$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  EXPECTED=$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | awk '{print $1}')
  ACTUAL=$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')
else
  info "⚠ no sha256sum/shasum available — skipping checksum verification"
  EXPECTED=""
  ACTUAL=""
fi
if [ -n "$EXPECTED" ] && [ "$EXPECTED" != "$ACTUAL" ]; then
  err "checksum mismatch (expected $EXPECTED got $ACTUAL)"
fi

# --- Extract + install -------------------------------------------------------
mkdir -p "$TMP/extract"
tar -xzf "$TMP/$ARCHIVE" -C "$TMP/extract"

mkdir -p "$PREFIX"
mv "$TMP/extract/$BIN_NAME" "$PREFIX/$BIN_NAME"
chmod +x "$PREFIX/$BIN_NAME"

info "✓ Installed $BIN_NAME $TAG → $PREFIX/$BIN_NAME"

# Path hint
case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *)
    info ""
    info "Add $PREFIX to your PATH:"
    info "  echo 'export PATH=\"$PREFIX:\$PATH\"' >> ~/.bashrc"
    info "  echo 'export PATH=\"$PREFIX:\$PATH\"' >> ~/.zshrc"
    ;;
esac

info ""
info "Get started:"
info "  browzer login"
info "  browzer init"
