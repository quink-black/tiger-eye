#!/usr/bin/env bash
# Build tiger-eye and install it to ~/bin.
set -euo pipefail

cd "$(dirname "$0")"

BIN_DIR="${BIN_DIR:-$HOME/bin}"
mkdir -p "$BIN_DIR"

echo "building tiger-eye..."
go build -o "$BIN_DIR/tiger-eye" .

# Re-sign on macOS so AMFI does not SIGKILL after the binary changes.
if [[ "$(uname -s)" == Darwin ]] && command -v codesign &>/dev/null; then
	codesign -s - "$BIN_DIR/tiger-eye" 2>/dev/null || true
fi

echo "installed -> $BIN_DIR/tiger-eye"
