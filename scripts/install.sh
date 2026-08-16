#!/bin/sh
set -eu

# Install only the diverge CLI binary.
# The controller and proxy are deployed via Helm chart, not installed locally.

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

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --connect-timeout 10 --max-time 120 "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    error "Neither curl nor wget found"
    exit 1
  fi
}

main() {
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
    if command -v curl >/dev/null 2>&1; then
        LATEST_URL=$(curl -fsSLI --connect-timeout 10 --max-time 120 -o /dev/null -w "%{url_effective}" https://github.com/divergedev/diverge/releases/latest)
    elif command -v wget >/dev/null 2>&1; then
        LATEST_URL=$(wget -q --server-response -O /dev/null https://github.com/divergedev/diverge/releases/latest 2>&1 | awk '/^  Location: /{print $2}' | tail -n1)
    else
        error "Neither curl nor wget found"
        exit 1
    fi
    TAG="${LATEST_URL##*/}"

    if [ -z "$TAG" ]; then
        error "Failed to fetch the latest version."
        exit 1
    fi

    VERSION=${TAG#v}
    info "Latest version is ${TAG}"

    FILE="diverge_${VERSION}_${os}_${arch}.tar.gz"
    URL="https://github.com/divergedev/diverge/releases/download/${TAG}/${FILE}"
    CHECKSUM_URL="https://github.com/divergedev/diverge/releases/download/${TAG}/checksums.txt"

    TMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TMP_DIR"' EXIT
    info "Downloading $FILE..."
    download "$URL" "$TMP_DIR/$FILE"

    info "Verifying checksum..."
    download "$CHECKSUM_URL" "$TMP_DIR/checksums.txt"
    EXPECTED=$(grep "$FILE" "$TMP_DIR/checksums.txt" | awk '{print $1}')
    if command -v sha256sum >/dev/null 2>&1; then
        ACTUAL=$(sha256sum "$TMP_DIR/$FILE" | awk '{print $1}')
    else
        ACTUAL=$(shasum -a 256 "$TMP_DIR/$FILE" | awk '{print $1}')
    fi
    if [ "$EXPECTED" != "$ACTUAL" ]; then
        error "Checksum verification failed!"
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

    case ":$PATH:" in
        *":$INSTALL_DIR:"*)
            success "diverge was installed successfully!"
            ;;
        *)
            success "diverge was installed successfully to $INSTALL_DIR!"
            info "Please add $INSTALL_DIR to your PATH"
            ;;
    esac

    "$INSTALL_DIR/diverge" --version 2>/dev/null || "$INSTALL_DIR/diverge" version 2>/dev/null || true
}

main
