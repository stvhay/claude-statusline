#!/usr/bin/env bash
set -euo pipefail

REPO="stvhay/claude-statusline"
INSTALL_DIR="${CLAUDE_HOME:-$HOME/.claude}/bin"
BINARY_NAME="statusline-command"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin|linux|freebsd|openbsd|netbsd) ;;
  mingw*|msys*|cygwin*) OS="windows" ;;
  *)
    echo "Unsupported OS: $OS"
    echo "You can build from source instead: go build -o statusline . && mv statusline $INSTALL_DIR/$BINARY_NAME"
    exit 1
    ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  i386|i686) ARCH="386" ;;
  armv7l) ARCH="armv7" ;;
  armv6l) ARCH="armv6" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    echo "You can build from source instead: go build -o statusline . && mv statusline $INSTALL_DIR/$BINARY_NAME"
    exit 1
    ;;
esac

# Determine archive extension
EXT="tar.gz"
if [ "$OS" = "windows" ]; then
  EXT="zip"
fi

# Get latest version tag
echo "Fetching latest release..."
if command -v curl &>/dev/null; then
  LATEST_TAG=$(curl -sf "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed 's/.*"tag_name": *"//;s/".*//')
elif command -v wget &>/dev/null; then
  LATEST_TAG=$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed 's/.*"tag_name": *"//;s/".*//')
else
  echo "Error: curl or wget is required"
  exit 1
fi

if [ -z "$LATEST_TAG" ]; then
  echo "Could not determine latest release."
  echo "You can build from source instead: go build -o statusline . && mv statusline $INSTALL_DIR/$BINARY_NAME"
  exit 1
fi

VERSION="${LATEST_TAG#v}"
ARCHIVE="statusline_${VERSION}_${OS}_${ARCH}.${EXT}"
URL="https://github.com/$REPO/releases/download/$LATEST_TAG/$ARCHIVE"

echo "Downloading $ARCHIVE..."
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

if command -v curl &>/dev/null; then
  if ! curl -sfL -o "$WORK_DIR/$ARCHIVE" "$URL"; then
    echo "Download failed. Your OS/architecture ($OS/$ARCH) may not have a pre-built binary."
    echo "You can build from source instead: go build -o statusline . && mv statusline $INSTALL_DIR/$BINARY_NAME"
    exit 1
  fi
elif command -v wget &>/dev/null; then
  if ! wget -q -O "$WORK_DIR/$ARCHIVE" "$URL"; then
    echo "Download failed. Your OS/architecture ($OS/$ARCH) may not have a pre-built binary."
    echo "You can build from source instead: go build -o statusline . && mv statusline $INSTALL_DIR/$BINARY_NAME"
    exit 1
  fi
fi

# Extract binary
echo "Installing to $INSTALL_DIR/$BINARY_NAME"
mkdir -p "$INSTALL_DIR"
if [ "$EXT" = "zip" ]; then
  unzip -o -q "$WORK_DIR/$ARCHIVE" -d "$WORK_DIR/extract"
else
  mkdir -p "$WORK_DIR/extract"
  tar -xzf "$WORK_DIR/$ARCHIVE" -C "$WORK_DIR/extract"
fi

# Find and install the binary
EXTRACTED_BIN="$WORK_DIR/extract/statusline"
if [ "$OS" = "windows" ]; then
  EXTRACTED_BIN="$WORK_DIR/extract/statusline.exe"
fi

if [ ! -f "$EXTRACTED_BIN" ]; then
  echo "Error: could not find statusline binary in archive"
  exit 1
fi

mv "$EXTRACTED_BIN" "$INSTALL_DIR/$BINARY_NAME"
chmod +x "$INSTALL_DIR/$BINARY_NAME"

# Configure Claude Code settings
SETTINGS="${CLAUDE_HOME:-$HOME/.claude}/settings.json"
COMMAND="$INSTALL_DIR/$BINARY_NAME"
HOOK_COMMAND="$COMMAND --hook"

if command -v jq &>/dev/null && [ -f "$SETTINGS" ]; then
  tmp=$(mktemp)
  jq --arg cmd "$COMMAND" --arg hook "$HOOK_COMMAND" '
    .statusLine = {"type": "command", "command": $cmd} |
    .hooks.UserPromptSubmit = [{"matcher": "", "hooks": [{"type": "command", "command": $hook}]}]
  ' "$SETTINGS" > "$tmp" && mv "$tmp" "$SETTINGS"
  echo "Updated $SETTINGS"
else
  echo "Add to ~/.claude/settings.json:"
  echo ""
  echo "  \"statusLine\": {"
  echo "    \"type\": \"command\","
  echo "    \"command\": \"$COMMAND\""
  echo "  },"
  echo "  \"hooks\": {"
  echo "    \"UserPromptSubmit\": [{"
  echo "      \"matcher\": \"\","
  echo "      \"hooks\": [{"
  echo "        \"type\": \"command\","
  echo "        \"command\": \"$HOOK_COMMAND\""
  echo "      }]"
  echo "    }]"
  echo "  }"
fi

echo "Done."
