#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 <version> --repo <owner/repo> [--tap <owner/tap>] [--force]"
  echo "  e.g. $0 v0.1.0 --repo kirby88/vix"
  echo ""
  echo "  --tap <owner/tap>  Homebrew tap repo (default: derives owner/homebrew-vix from --repo)"
  echo "  --force            Replace an existing release"
  exit 1
}

# Parse arguments
VERSION=""
REPO=""
TAP=""
FORCE=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      REPO="$2"
      shift 2
      ;;
    --tap)
      TAP="$2"
      shift 2
      ;;
    --force)
      FORCE=true
      shift
      ;;
    -h|--help)
      usage
      ;;
    *)
      if [[ -z "$VERSION" ]]; then
        VERSION="$1"
      else
        echo "Unknown argument: $1"
        usage
      fi
      shift
      ;;
  esac
done

if [[ -z "$VERSION" || -z "$REPO" ]]; then
  usage
fi

# Derive tap from repo owner if not specified
if [[ -z "$TAP" ]]; then
  OWNER="${REPO%%/*}"
  TAP="${OWNER}/homebrew-vix"
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# --- Step 0: Ensure git working tree is clean ---
echo "==> Checking git status..."
if [[ -n "$(git status --porcelain)" ]]; then
  echo "!! Git working tree is not clean. Please commit or stash your changes before releasing."
  git status --short
  exit 1
fi
echo "==> Git working tree is clean."
echo ""

# --- Step 1: Verify YubiKey via SSH ---
echo "==> Checking YubiKey (SSH)..."
SSH_OUTPUT=$(ssh -T git@github.com 2>&1 || true)
if ! echo "$SSH_OUTPUT" | grep -qi "successfully authenticated"; then
  echo "!! SSH authentication to GitHub failed. Is your YubiKey inserted?"
  echo "   $SSH_OUTPUT"
  exit 1
fi
echo "==> YubiKey confirmed."
echo ""

# --- Step 2: Build ---
"$SCRIPT_DIR/build.sh" "$VERSION" --repo "$REPO"

# --- Step 3: Pause for optional local testing ---
echo ""
echo "================================================"
echo "  Build complete. You can test locally with:"
echo "    ./script/test-install.sh --local"
echo "================================================"
echo ""
read -rp "Press Enter to continue with publish, or Ctrl-C to abort... "

# --- Step 4: Publish ---
PUBLISH_ARGS=("$VERSION" --repo "$REPO")
if [[ "$FORCE" == true ]]; then
  PUBLISH_ARGS+=(--force)
fi
"$SCRIPT_DIR/publish.sh" "${PUBLISH_ARGS[@]}"

# --- Step 5: Update tap ---
"$SCRIPT_DIR/update-tap.sh" "$VERSION" --tap "$TAP"

# --- Step 6: Tag the release commit ---
echo ""
echo "==> Tagging commit as $VERSION..."
git tag "$VERSION"
echo "==> Tagged $VERSION."

# --- Done ---
echo ""
echo "================================================"
echo "  Vix $VERSION released successfully!"
echo ""
echo "  Install with:"
echo "    brew tap ${TAP%%/*}/vix"
echo "    brew install vix"
echo "================================================"
