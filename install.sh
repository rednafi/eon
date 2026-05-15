#!/bin/sh
# eon installer for Linux. Detects arch, downloads the matching
# release binary, and installs it to /usr/local/bin (or $EON_PREFIX/bin
# if set). Pinned to v0.1.0 by default; pass EON_VERSION to override.

set -eu

EON_VERSION="${EON_VERSION:-v0.1.0}"
EON_PREFIX="${EON_PREFIX:-/usr/local}"
INSTALL_DIR="${EON_PREFIX}/bin"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

uname_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$uname_os" in
    linux) os="linux" ;;
    darwin)
        echo "install.sh is for Linux; on macOS use:"
        echo "  brew tap rednafi/eon https://github.com/rednafi/eon"
        echo "  brew install eon"
        exit 1
        ;;
    *)
        echo "unsupported OS: $uname_os" >&2
        exit 1
        ;;
esac

uname_arch="$(uname -m)"
case "$uname_arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *)
        echo "unsupported arch: $uname_arch" >&2
        exit 1
        ;;
esac

asset="eon-${EON_VERSION}-${os}-${arch}.tar.gz"
url="https://github.com/rednafi/eon/releases/download/${EON_VERSION}/${asset}"
sums_url="https://github.com/rednafi/eon/releases/download/${EON_VERSION}/sha256sums.txt"

echo "Downloading $asset..."
curl -fsSL "$url" -o "$TMPDIR/$asset"
curl -fsSL "$sums_url" -o "$TMPDIR/sha256sums.txt"

echo "Verifying checksum..."
(cd "$TMPDIR" && grep " $asset\$" sha256sums.txt | sha256sum -c -)

tar -xzf "$TMPDIR/$asset" -C "$TMPDIR"

if [ ! -w "$INSTALL_DIR" ]; then
    echo "Installing to $INSTALL_DIR (requires sudo)..."
    sudo install -m 0755 "$TMPDIR/eon" "$INSTALL_DIR/eon"
else
    install -m 0755 "$TMPDIR/eon" "$INSTALL_DIR/eon"
fi

echo "Installed eon $EON_VERSION to $INSTALL_DIR/eon"
"$INSTALL_DIR/eon" --version || true
