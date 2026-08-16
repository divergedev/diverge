#!/bin/sh
set -e

# Diverge Environment Engine Operator Installer

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

if [ "$ARCH" = "x86_64" ]; then
    ARCH="amd64"
elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    ARCH="arm64"
else
    echo "Unsupported architecture: $ARCH"
    exit 1
fi

REPO="divergedev/diverge"
LATEST_RELEASE_URL="https://api.github.com/repos/$REPO/releases/latest"

echo "Fetching latest release from $LATEST_RELEASE_URL..."
DOWNLOAD_URL=$(curl -s $LATEST_RELEASE_URL | grep "browser_download_url" | grep "diverge_${OS}_${ARCH}.tar.gz" | cut -d '"' -f 4 | head -n 1)

if [ -z "$DOWNLOAD_URL" ]; then
    echo "Could not find a release for $OS $ARCH."
    exit 1
fi

echo "Downloading $DOWNLOAD_URL..."
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT
curl -sL "$DOWNLOAD_URL" -o "$TMP_DIR/diverge.tar.gz"

echo "Extracting..."
tar -xzf "$TMP_DIR/diverge.tar.gz" -C "$TMP_DIR"

INSTALL_DIR="/usr/local/bin"
if [ ! -w "$INSTALL_DIR" ]; then
    INSTALL_DIR="$HOME/.local/bin"
    mkdir -p "$INSTALL_DIR"
fi

echo "Installing diverge to $INSTALL_DIR..."
mv "$TMP_DIR/diverge" "$INSTALL_DIR/diverge"
chmod +x "$INSTALL_DIR/diverge"

echo "Installation complete."

"$INSTALL_DIR/diverge" version

if ! command -v diverge >/dev/null 2>&1; then
    echo "Please add $INSTALL_DIR to your PATH."
fi
