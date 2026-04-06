#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${HOME}/.local/bin"

echo "Building wsm..."
cd "$(dirname "$0")"
go build -o wsm .

echo "Installing to ${INSTALL_DIR}/wsm..."
mkdir -p "$INSTALL_DIR"
mv wsm "$INSTALL_DIR/wsm"

echo "Done! Make sure ${INSTALL_DIR} is in your PATH."
echo ""
echo "tmux binding (add to ~/.tmux.conf):"
echo '  bind-key w run-shell "tmux popup -E -w 80% -h 80% wsm"'
