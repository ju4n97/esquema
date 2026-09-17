#!/usr/bin/env bash
set -euo pipefail

REPO="ju4n97/hclapi"
INSTALL_DIR="/usr/local/bin"
MAN_DIR="/usr/local/share/man/man1"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

case "$OS" in
  linux|darwin|freebsd) ;;
  *) echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# Fetch latest release version
LATEST_TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
TAG_NO_V="${LATEST_TAG#v}"
FILENAME="hclapi_${TAG_NO_V}_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${FILENAME}"
MAN_URL="https://raw.githubusercontent.com/${REPO}/${LATEST_TAG}/man/hclapi.1"

echo "Downloading hclapi ${LATEST_TAG} for ${OS}/${ARCH}..."
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

# Download and extract binary
curl -fsSL "$DOWNLOAD_URL" | tar -xz -C "$TMP_DIR"

# Install binary to system PATH
if [[ -w "$INSTALL_DIR" ]]; then
  install -m 0755 "$TMP_DIR/hclapi" "$INSTALL_DIR/hclapi"
else
  echo "Elevated permissions required to install binary to $INSTALL_DIR:"
  sudo install -m 0755 "$TMP_DIR/hclapi" "$INSTALL_DIR/hclapi"
fi

# Download and install manual page
if curl -fsSL "$MAN_URL" -o "$TMP_DIR/hclapi.1" 2>/dev/null; then
  echo "Installing manual page to $MAN_DIR..."
  if [[ -w "/usr/local/share" || -w "$MAN_DIR" ]]; then
    mkdir -p "$MAN_DIR"
    install -m 0644 "$TMP_DIR/hclapi.1" "$MAN_DIR/hclapi.1"
  else
    sudo mkdir -p "$MAN_DIR"
    sudo install -m 0644 "$TMP_DIR/hclapi.1" "$MAN_DIR/hclapi.1"
  fi
  
  # Update local man database if mandb is present
  if command -v mandb >/dev/null 2>&1; then
    sudo mandb 2>/dev/null || true
  fi
fi

echo
echo "hclapi was installed successfully!"
echo "  • Binary:   $INSTALL_DIR/hclapi"
echo "  • Man page: $MAN_DIR/hclapi.1"