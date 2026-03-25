#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 <version> --repo <owner/repo> [--force]"
  echo "  e.g. $0 v0.1.0 --repo kirby88/vix"
  echo ""
  echo "  --force   Delete existing release before creating a new one"
  exit 1
}

# Parse arguments
VERSION=""
REPO=""
FORCE=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      REPO="$2"
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

# Ensure version starts with v
if [[ "$VERSION" != v* ]]; then
  VERSION="v$VERSION"
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
DIST_DIR="$ROOT_DIR/dist"

# Verify artifacts exist
TARBALLS=("$DIST_DIR"/vix-*.tar.gz)
if [[ ${#TARBALLS[@]} -eq 0 || ! -f "${TARBALLS[0]}" ]]; then
  echo "No dist/*.tar.gz files found. Run build.sh first."
  exit 1
fi

# Handle --force: delete existing release
if [[ "$FORCE" == true ]]; then
  echo "==> Deleting existing release $VERSION (if any)..."
  gh release delete "$VERSION" --repo "$REPO" --yes --cleanup-tag || true
fi

# Create release and upload tarballs
echo "==> Creating GitHub release $VERSION..."
gh release create "$VERSION" \
  --repo "$REPO" \
  --title "$VERSION" \
  --generate-notes \
  "${TARBALLS[@]}"

RELEASE_URL="https://github.com/${REPO}/releases/tag/${VERSION}"
echo ""
echo "==> Release published: $RELEASE_URL"
