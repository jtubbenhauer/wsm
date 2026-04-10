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
echo "tmux bindings (add to ~/.tmux.conf):"
echo '  bind-key s run-shell "tmux popup -E -w 80% -h 80% wsm"'
echo '  bind-key w display-popup -E -w 60% -h 40% "wsm switch"'
