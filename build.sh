#!/usr/bin/env bash
# Build tiger-eye and install it to ~/bin.
set -euo pipefail

cd "$(dirname "$0")"

BIN_DIR="${BIN_DIR:-$HOME/bin}"
mkdir -p "$BIN_DIR"

echo "building tiger-eye..."
go build -o "$BIN_DIR/tiger-eye" .

echo "installed -> $BIN_DIR/tiger-eye"
