#!/bin/bash
set -e

URL="https://github.com/hdmain/rawuploader/raw/refs/heads/main/tcpraw"
INSTALL_PATH="/usr/local/bin/tcpraw"
TMP_FILE="$(mktemp)"

echo "📥 Downloading latest tcpraw..."

if command -v curl >/dev/null 2>&1; then
    curl -L "$URL" -o "$TMP_FILE"
elif command -v wget >/dev/null 2>&1; then
    wget -O "$TMP_FILE" "$URL"
else
    echo "❌ Error: curl or wget is required."
    exit 1
fi

if [ ! -s "$TMP_FILE" ]; then
    echo "❌ Error: download failed."
    exit 1
fi

if [ -f "$INSTALL_PATH" ]; then
    echo "🔄 Updating existing installation..."
else
    echo "🔧 Installing tcpraw..."
fi

sudo install -m 755 "$TMP_FILE" "$INSTALL_PATH"

rm "$TMP_FILE"

echo "✅ Installation / Update completed successfully!"
echo "You can run the program using: tcpraw"
