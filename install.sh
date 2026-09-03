#!/bin/sh
set -eu

REPO="behnambm/gcli"
BASE_URL="${GCLI_BASE_URL:-https://github.com/$REPO/releases/download/latest}"
INSTALL_DIR="${GCLI_INSTALL_DIR:-/usr/local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"

case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "gcli: unsupported architecture: $arch" >&2; exit 1 ;;
esac

case "$os" in
  darwin|linux) ;;
  *) echo "gcli: unsupported OS: $os (supported: macOS, Linux)" >&2; exit 1 ;;
esac

binary="gcli-$os-$arch"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "gcli: downloading $binary ..."
curl -fsSL "$BASE_URL/$binary" -o "$tmp/gcli"
curl -fsSL "$BASE_URL/checksums.txt" -o "$tmp/checksums.txt"

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/gcli" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/gcli" | awk '{print $1}')"
fi
expected="$(awk -v b="$binary" '$2 == b {print $1}' "$tmp/checksums.txt")"
if [ -z "$expected" ] || [ "$actual" != "$expected" ]; then
  echo "gcli: checksum mismatch for $binary" >&2
  exit 1
fi

if ! mkdir -p "$INSTALL_DIR" 2>/dev/null || [ ! -w "$INSTALL_DIR" ]; then
  echo "gcli: $INSTALL_DIR not writable, falling back to $HOME/.local/bin"
  INSTALL_DIR="$HOME/.local/bin"
  mkdir -p "$INSTALL_DIR"
fi

install -m 0755 "$tmp/gcli" "$INSTALL_DIR/gcli"
echo "gcli: installed to $INSTALL_DIR/gcli"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "gcli: note — $INSTALL_DIR is not on your PATH" >&2 ;;
esac
