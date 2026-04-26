#!/usr/bin/env bash
# claude-mobile-agent installer for macOS.
# Usage: curl -fsSL https://raw.githubusercontent.com/chaohaowang/claude-mobile-agent/main/install.sh | bash
set -euo pipefail

REPO="chaohaowang/claude-mobile-agent"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "error: this installer only supports macOS" >&2
  exit 1
fi

case "$(uname -m)" in
  arm64)  ASSET="claude-mobile-darwin-arm64.tar.gz" ;;
  x86_64) ASSET="claude-mobile-darwin-amd64.tar.gz" ;;
  *) echo "error: unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

echo "→ fetching latest release of $REPO"
TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
  | grep '"tag_name"' | head -1 | cut -d'"' -f4)
if [[ -z "$TAG" ]]; then
  echo "error: could not determine latest release tag (is the repo public and has at least one release?)" >&2
  exit 1
fi
echo "  $TAG"

URL="https://github.com/$REPO/releases/download/$TAG/$ASSET"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "→ downloading $ASSET"
curl -fsSL "$URL" -o "$TMP/asset.tar.gz"
tar -xzf "$TMP/asset.tar.gz" -C "$TMP"

mkdir -p "$INSTALL_DIR"
mv "$TMP"/claude-mobile-darwin-*/claude-mobile "$INSTALL_DIR/claude-mobile"
chmod +x "$INSTALL_DIR/claude-mobile"
# Strip quarantine if it was applied (e.g. tarball was downloaded via Safari before piping here).
xattr -d com.apple.quarantine "$INSTALL_DIR/claude-mobile" 2>/dev/null || true

echo "✓ installed: $INSTALL_DIR/claude-mobile ($TAG)"

if ! echo ":$PATH:" | grep -q ":$INSTALL_DIR:"; then
  echo
  echo "Note: $INSTALL_DIR is not on your PATH. Add to ~/.zshrc:"
  echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

echo
echo "Next steps:"
echo "  1. claude-mobile pair          # prints QR + URL — open on iPhone"
echo "  2. tmux new-session -d -s work 'claude'"
echo "  3. claude-mobile daemon        # foreground bridge"
