#!/bin/sh
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

info() {
    printf "${BLUE}INFO${NC}: %s\n" "$1"
}

success() {
    printf "${GREEN}SUCCESS${NC}: %s\n" "$1"
}

error() {
    printf "${RED}ERROR${NC}: %s\n" "$1"
}

OS="$(uname -s)"
case "${OS}" in
    Linux*)     os=linux;;
    Darwin*)    os=darwin;;
    *)          error "Unsupported OS: ${OS}"; exit 1;;
esac

ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64)   arch=amd64;;
    aarch64)  arch=arm64;;
    arm64)    arch=arm64;;
    *)        error "Unsupported architecture: ${ARCH}"; exit 1;;
esac

info "Detecting OS: $os, architecture: $arch"

info "Fetching latest release version..."
LATEST_URL=$(curl -fsSLI -o /dev/null -w "%{url_effective}" https://github.com/divergedev/diverge/releases/latest)
TAG="${LATEST_URL##*/}"

if [ -z "$TAG" ]; then
    error "Failed to fetch the latest version."
    exit 1
fi

VERSION=${TAG#v}
info "Latest version is ${TAG}"

FILE="diverge_${VERSION}_${os}_${arch}.tar.gz"
URL="https://github.com/divergedev/diverge/releases/download/${TAG}/${FILE}"

TMP_DIR=$(mktemp -d)
info "Downloading $FILE..."
if ! curl -fsSL "$URL" -o "$TMP_DIR/$FILE"; then
    error "Download failed from $URL"
    exit 1
fi

if echo "$FILE" | grep -q '\.tar\.gz$'; then
    info "Extracting..."
    tar -xzf "$TMP_DIR/$FILE" -C "$TMP_DIR"
    BIN_PATH="$TMP_DIR/diverge"
else
    BIN_PATH="$TMP_DIR/$FILE"
fi

INSTALL_DIR="/usr/local/bin"
if [ ! -w "$INSTALL_DIR" ]; then
    INSTALL_DIR="$HOME/.local/bin"
    if [ ! -d "$INSTALL_DIR" ]; then
        mkdir -p "$INSTALL_DIR"
    fi
fi

info "Installing to $INSTALL_DIR..."
mv "$BIN_PATH" "$INSTALL_DIR/diverge"
chmod +x "$INSTALL_DIR/diverge"

rm -rf "$TMP_DIR"

if echo "$PATH" | grep -q "$INSTALL_DIR"; then
    success "diverge was installed successfully!"
else
    success "diverge was installed successfully to $INSTALL_DIR!"
    info "Please add $INSTALL_DIR to your PATH"
fi

"$INSTALL_DIR/diverge" --version 2>/dev/null || "$INSTALL_DIR/diverge" version 2>/dev/null || true
