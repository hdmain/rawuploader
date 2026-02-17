#!/bin/bash
set -e

URL="https://github.com/hdmain/rawuploader/raw/refs/heads/main/tcpraw"
TMP_FILE="$(mktemp)"

echo "📥 Downloading tcpraw..."
curl -L "$URL" -o "$TMP_FILE"

if [ ! -s "$TMP_FILE" ]; then
    echo "❌ Error: file was not downloaded."
    exit 1
fi

echo "🔧 Installing to /usr/local/bin..."
sudo install -m 755 "$TMP_FILE" /usr/local/bin/tcpraw

rm "$TMP_FILE"

echo "✅ Installation completed successfully!"
echo "You can now run the program using: tcpraw"
