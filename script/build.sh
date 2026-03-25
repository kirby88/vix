#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 <version> --repo <owner/repo>"
  echo "  e.g. $0 v0.1.0 --repo kirby88/vix"
  exit 1
}

# Parse arguments
VERSION=""
REPO=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      REPO="$2"
      shift 2
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

echo "==> Building Vix $VERSION"

# Clean and create dist/
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

# --- macOS (native, CGO required for tree-sitter) ---
echo "==> Compiling for darwin-arm64"
BUILD_DIR="$DIST_DIR/vix-darwin-arm64"
mkdir -p "$BUILD_DIR"
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o "$BUILD_DIR/vix" "$ROOT_DIR/cmd/vix"
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o "$BUILD_DIR/vix-daemon" "$ROOT_DIR/cmd/vix-daemon"
tar -czf "$DIST_DIR/vix-darwin-arm64.tar.gz" -C "$DIST_DIR" "vix-darwin-arm64"
rm -rf "$BUILD_DIR"

# --- Linux (built via Docker for CGO cross-compilation) ---
build_linux() {
  local arch="$1"
  local platform="linux/${arch}"
  local label="linux-${arch}"

  echo "==> Compiling for $label (via Docker)"
  docker build --platform "$platform" -f - -t "vix-build-${label}" "$ROOT_DIR" <<'DOCKERFILE'
FROM golang:1.26-bookworm
COPY . /src
WORKDIR /src
RUN mkdir -p /out \
    && go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /out/vix ./cmd/vix \
    && go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /out/vix-daemon ./cmd/vix-daemon
DOCKERFILE

  BUILD_DIR="$DIST_DIR/vix-${label}"
  mkdir -p "$BUILD_DIR"
  docker create --name "vix-extract-${label}" "vix-build-${label}" true
  docker cp "vix-extract-${label}:/out/vix" "$BUILD_DIR/vix"
  docker cp "vix-extract-${label}:/out/vix-daemon" "$BUILD_DIR/vix-daemon"
  docker rm "vix-extract-${label}"

  tar -czf "$DIST_DIR/vix-${label}.tar.gz" -C "$DIST_DIR" "vix-${label}"
  rm -rf "$BUILD_DIR"
}

build_linux arm64
build_linux amd64

# --- Checksums ---
echo "==> Computing checksums"
sha_of() { shasum -a 256 "$DIST_DIR/vix-${1}.tar.gz" | awk '{print $1}'; }
SHA_DARWIN_ARM64=$(sha_of darwin-arm64)
SHA_LINUX_ARM64=$(sha_of linux-arm64)
SHA_LINUX_AMD64=$(sha_of linux-amd64)
echo "  darwin-arm64: $SHA_DARWIN_ARM64"
echo "  linux-arm64:  $SHA_LINUX_ARM64"
echo "  linux-amd64:  $SHA_LINUX_AMD64"

# --- Homebrew formula ---
RELEASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

cat > "$DIST_DIR/vix.rb" <<FORMULA
class Vix < Formula
  desc "AI coding agent"
  homepage "https://github.com/${REPO}"
  version "${VERSION#v}"
  license :cannot_represent

  on_macos do
    on_arm do
      url "${RELEASE_URL}/vix-darwin-arm64.tar.gz"
      sha256 "${SHA_DARWIN_ARM64}"
    end
  end

  on_linux do
    on_arm do
      url "${RELEASE_URL}/vix-linux-arm64.tar.gz"
      sha256 "${SHA_LINUX_ARM64}"
    end
    on_intel do
      url "${RELEASE_URL}/vix-linux-amd64.tar.gz"
      sha256 "${SHA_LINUX_AMD64}"
    end
  end

  def install
    bin.install "vix"
    bin.install "vix-daemon"
  end

  service do
    run [opt_bin/"vix-daemon"]
    keep_alive true
    log_path var/"log/vix-daemon.log"
    error_log_path var/"log/vix-daemon.log"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/vix --version 2>&1", 1)
  end
end
FORMULA

# --- Local test formula (file:// URLs for test-install.sh) ---
cat > "$DIST_DIR/vix-local.rb" <<FORMULA
class Vix < Formula
  desc "AI coding agent"
  homepage "https://github.com/${REPO}"
  version "${VERSION#v}"
  license :cannot_represent

  on_macos do
    on_arm do
      url "file:///tmp/dist/vix-darwin-arm64.tar.gz"
      sha256 "${SHA_DARWIN_ARM64}"
    end
  end

  on_linux do
    on_arm do
      url "file:///tmp/dist/vix-linux-arm64.tar.gz"
      sha256 "${SHA_LINUX_ARM64}"
    end
    on_intel do
      url "file:///tmp/dist/vix-linux-amd64.tar.gz"
      sha256 "${SHA_LINUX_AMD64}"
    end
  end

  def install
    bin.install "vix"
    bin.install "vix-daemon"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/vix --version 2>&1", 1)
  end
end
FORMULA

echo ""
echo "==> Build complete! Artifacts in dist/:"
ls -lh "$DIST_DIR"
