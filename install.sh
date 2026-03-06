#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${CLAUDE_HOME:-$HOME/.claude}/bin"
BINARY_NAME="statusline-command"

echo "Building statusline..."
go build -o "$BINARY_NAME" .

echo "Installing to $INSTALL_DIR/$BINARY_NAME"
mkdir -p "$INSTALL_DIR"
mv "$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"

SETTINGS="${CLAUDE_HOME:-$HOME/.claude}/settings.json"
COMMAND="$INSTALL_DIR/$BINARY_NAME"

if command -v jq &>/dev/null && [ -f "$SETTINGS" ]; then
  tmp=$(mktemp)
  jq --arg cmd "$COMMAND" '.statusLine = {"type": "command", "command": $cmd}' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
  echo "Updated $SETTINGS"
else
  echo "Add to ~/.claude/settings.json:"
  echo ""
  echo "  \"statusLine\": {"
  echo "    \"type\": \"command\","
  echo "    \"command\": \"$COMMAND\""
  echo "  }"
fi

echo "Done."
