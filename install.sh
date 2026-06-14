#!/bin/sh
# Donate Your Code — one-line installer.
#   curl -fsSL https://raw.githubusercontent.com/GeeveGeorge/donate-your-code/main/install.sh | sh
# Detects your OS/arch, downloads the matching dyc release binary, verifies its
# SHA-256 against the published CHECKSUMS, and drops a ./dyc in the current dir.
set -eu

REPO="GeeveGeorge/donate-your-code"
BASE="https://github.com/$REPO/releases/latest/download"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "dyc: unsupported arch '$arch' — use: go install github.com/$REPO/cmd/dyc@latest" >&2; exit 1 ;;
esac
case "$os" in
  darwin) asset="dyc-darwin-$arch" ;;
  linux)  asset="dyc-linux-$arch" ;;
  *) echo "dyc: unsupported OS '$os' — download dyc-windows-amd64.exe from the releases page, or use go install." >&2; exit 1 ;;
esac

echo "dyc: downloading $asset ..." >&2
curl -fsSL -o dyc "$BASE/$asset"
curl -fsSL -o CHECKSUMS.txt "$BASE/CHECKSUMS.txt"

want=$(grep " $asset\$" CHECKSUMS.txt | awk '{print $1}')
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum dyc | awk '{print $1}')
else
  got=$(shasum -a 256 dyc | awk '{print $1}')
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "dyc: CHECKSUM MISMATCH — refusing to use this binary." >&2
  echo "  expected: $want" >&2
  echo "  got:      $got" >&2
  rm -f dyc
  exit 1
fi
chmod +x dyc
rm -f CHECKSUMS.txt
echo "dyc: verified and installed -> ./dyc" >&2
echo "Next: ./dyc scan" >&2
