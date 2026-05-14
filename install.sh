#!/bin/bash
set -e

REPO="hdmain/rawuploader"
ASSET="tcpraw-linux-amd64"
URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
INSTALL_PATH="/usr/local/bin/tcpraw"
TMP_FILE="$(mktemp)"

echo "Downloading latest tcpraw from GitHub release (${ASSET})..."

if command -v curl >/dev/null 2>&1; then
    if ! curl -fsSL "$URL" -o "$TMP_FILE"; then
        echo "Error: download failed. Is there a published release with this asset?"
        echo "  $URL"
        exit 1
    fi
elif command -v wget >/dev/null 2>&1; then
    if ! wget -q -O "$TMP_FILE" "$URL"; then
        echo "Error: download failed. Is there a published release with this asset?"
        echo "  $URL"
        exit 1
    fi
else
    echo "Error: curl or wget is required."
    exit 1
fi

if [ ! -s "$TMP_FILE" ]; then
    echo "Error: downloaded file is empty."
    exit 1
fi

if [ -f "$INSTALL_PATH" ]; then
    echo "Updating existing installation..."
else
    echo "Installing tcpraw..."
fi

sudo install -m 755 "$TMP_FILE" "$INSTALL_PATH"

rm "$TMP_FILE"

echo "Installation / update completed successfully."
echo "You can run the program using: tcpraw"
