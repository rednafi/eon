#!/bin/sh
# eon installer for Linux. Detects arch, downloads the matching
# release binary, and installs it to /usr/local/bin (or $EON_PREFIX/bin
# if set). Defaults to the latest GitHub release; pass EON_VERSION to
# install a specific version.

set -eu

EON_VERSION="${EON_VERSION:-}"
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

if [ -z "$EON_VERSION" ] || [ "$EON_VERSION" = "latest" ]; then
    echo "Resolving latest eon release..."
    latest_json="$(curl -fsSL https://api.github.com/repos/rednafi/eon/releases/latest)" || {
        echo "failed to resolve latest eon release" >&2
        exit 1
    }
    EON_VERSION="$(printf '%s\n' "$latest_json" |
        sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
        head -n 1)"
    if [ -z "$EON_VERSION" ]; then
        echo "failed to parse latest eon release tag" >&2
        exit 1
    fi
fi

case "$EON_VERSION" in
    v*) ;;
    *) EON_VERSION="v$EON_VERSION" ;;
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
