#!/bin/sh
# Installer for the L80 CLI — https://launch80.com/skills
#
# Usage:
#   curl -fsSL https://launch80.com/install.sh | sh
#   curl -fsSL https://launch80.com/install.sh | sh -s -- --prefix /usr/local/bin
#
# Reads nothing, sends nothing, and installs exactly one file.

set -eu

REPO="launch80/L80-CLI"
BINARY="L80"
PREFIX="${L80_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="latest"

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) PREFIX="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    -h|--help)
      echo "usage: install.sh [--prefix DIR] [--version vX.Y.Z]"
      exit 0 ;;
    *) echo "install.sh: unknown option $1" >&2; exit 2 ;;
  esac
done

die() { echo "install.sh: $*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed."
}

need uname
need tar
need mktemp

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
else
  die "either curl or wget is required."
fi

# --- platform -----------------------------------------------------------------

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)

case "$os" in
  linux|darwin) ;;
  *) die "unsupported operating system: $os. Build from source instead: https://github.com/$REPO" ;;
esac

case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) die "unsupported architecture: $arch. Build from source instead: https://github.com/$REPO" ;;
esac

asset="${BINARY}_${os}_${arch}.tar.gz"

if [ "$VERSION" = "latest" ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

# --- download -----------------------------------------------------------------

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "==> downloading $asset ($VERSION)"
fetch "$base/$asset" "$tmp/$asset" \
  || die "could not download $base/$asset — check that the release exists."

# --- verify -------------------------------------------------------------------

if fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    sha_cmd="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    sha_cmd="shasum -a 256"
  else
    sha_cmd=""
  fi

  if [ -n "$sha_cmd" ]; then
    expected=$(grep " $asset\$" "$tmp/checksums.txt" | awk '{print $1}')
    [ -n "$expected" ] || die "no checksum listed for $asset."
    actual=$($sha_cmd "$tmp/$asset" | awk '{print $1}')
    [ "$expected" = "$actual" ] || die "checksum mismatch for $asset — refusing to install."
    echo "==> checksum verified"
  else
    echo "==> no sha256 tool found; skipping checksum verification" >&2
  fi
else
  echo "==> checksums.txt unavailable; skipping verification" >&2
fi

# --- install ------------------------------------------------------------------

tar -xzf "$tmp/$asset" -C "$tmp" || die "could not extract $asset."
[ -f "$tmp/$BINARY" ] || die "archive did not contain $BINARY."

mkdir -p "$PREFIX" || die "could not create $PREFIX."
install -m 0755 "$tmp/$BINARY" "$PREFIX/$BINARY" 2>/dev/null \
  || { cp "$tmp/$BINARY" "$PREFIX/$BINARY" && chmod 0755 "$PREFIX/$BINARY"; } \
  || die "could not write to $PREFIX. Try: --prefix \$HOME/.local/bin"

echo "==> installed $BINARY to $PREFIX/$BINARY"

# The command is L80, but muscle memory says l80. On a case-sensitive
# filesystem (most Linux) add a lowercase alias so both work. On a
# case-insensitive one (default macOS) the path already exists and points at
# the binary we just wrote, so linking would target the file itself — skip it.
if [ ! -e "$PREFIX/l80" ]; then
  ln -s "$BINARY" "$PREFIX/l80" 2>/dev/null \
    && echo "==> linked $PREFIX/l80 -> $BINARY" || true
fi

case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *)
    echo ""
    echo "$PREFIX is not on your PATH. Add it:"
    echo ""
    echo "  echo 'export PATH=\"$PREFIX:\$PATH\"' >> ~/.zshrc && exec \$SHELL"
    echo ""
    ;;
esac

echo "next: save your publish token (https://launch80.com/skills#token), then run $BINARY doctor"
