#!/bin/bash
# Cross-compile Serv for all target platforms.
# Usage: ./release-scripts/build-release.sh v1.0.0
#
# Prerequisites: Go 1.18+ installed
# Outputs: release/ directory with platform-specific archives

set -e

VERSION=${1:-"dev"}
OUTDIR="release"
MODULE="serv"

echo "Building Serv $VERSION for all platforms..."
mkdir -p "$OUTDIR"

# Platforms: os/arch
PLATFORMS=(
    "darwin/amd64"
    "darwin/arm64"
    "linux/amd64"
    "linux/arm64"
    "windows/amd64"
)

for PLATFORM in "${PLATFORMS[@]}"; do
    GOOS="${PLATFORM%/*}"
    GOARCH="${PLATFORM#*/}"
    
    EXT=""
    if [ "$GOOS" = "windows" ]; then
        EXT=".exe"
    fi

    BINARY_NAME="serv${EXT}"
    LSP_BINARY="serv-lsp${EXT}"
    ARCHIVE_NAME="serv-${GOOS}-${GOARCH}"

    echo "  Building $GOOS/$GOARCH..."
    
    # Build compiler
    GOOS=$GOOS GOARCH=$GOARCH go build -ldflags="-s -w -X main.version=$VERSION" -o "$OUTDIR/$BINARY_NAME" main.go
    
    # Build LSP
    GOOS=$GOOS GOARCH=$GOARCH go build -ldflags="-s -w" -o "$OUTDIR/$LSP_BINARY" ./lsp/

    # Package
    if [ "$GOOS" = "windows" ]; then
        (cd "$OUTDIR" && zip "${ARCHIVE_NAME}.zip" "$BINARY_NAME" "$LSP_BINARY")
    else
        (cd "$OUTDIR" && tar czf "${ARCHIVE_NAME}.tar.gz" "$BINARY_NAME" "$LSP_BINARY")
    fi

    # Cleanup binaries (keep archives)
    rm -f "$OUTDIR/$BINARY_NAME" "$OUTDIR/$LSP_BINARY"
done

echo ""
echo "Release archives:"
ls -la "$OUTDIR"/*.{tar.gz,zip} 2>/dev/null
echo ""
echo "Next steps:"
echo "  1. Create GitHub release with tag $VERSION"
echo "  2. Upload archives from release/"
echo "  3. Update sha256 hashes in homebrew/serv.rb and scoop/serv.json"
