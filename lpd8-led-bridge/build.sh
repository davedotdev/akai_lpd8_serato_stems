#!/bin/bash

# Build script for lpd8-led-bridge
# Note: rtmidi driver uses CGO, so cross-compilation requires native toolchains
# For full cross-compilation, build on each target platform or use Docker

set -e

VERSION=${1:-"dev"}
OUTPUT_DIR="releases"
APP_NAME="lpd8-led-bridge"

echo "Building $APP_NAME version $VERSION..."
echo ""

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Detect current platform
CURRENT_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
CURRENT_ARCH=$(uname -m)

# Map architecture names
case "$CURRENT_ARCH" in
    x86_64) CURRENT_ARCH="amd64" ;;
    arm64|aarch64) CURRENT_ARCH="arm64" ;;
    i386|i686) CURRENT_ARCH="386" ;;
esac

case "$CURRENT_OS" in
    darwin) CURRENT_OS="darwin" ;;
    linux) CURRENT_OS="linux" ;;
    mingw*|msys*|cygwin*) CURRENT_OS="windows" ;;
esac

echo "Detected platform: $CURRENT_OS/$CURRENT_ARCH"
echo ""

# Build for current platform
echo "Building for $CURRENT_OS/$CURRENT_ARCH..."
if [ "$CURRENT_OS" = "windows" ]; then
    EXT=".exe"
else
    EXT=""
fi

go build -ldflags "-X main.Version=$VERSION" -o "$OUTPUT_DIR/${APP_NAME}-${CURRENT_OS}-${CURRENT_ARCH}${EXT}" .

# Generate default config
echo "Generating default config..."
"$OUTPUT_DIR/${APP_NAME}-${CURRENT_OS}-${CURRENT_ARCH}${EXT}" -genconfig "$OUTPUT_DIR/config.json"

echo ""
echo "Build complete! Files in $OUTPUT_DIR/:"
ls -la "$OUTPUT_DIR/"

echo ""
echo "Note: Due to CGO dependencies (rtmidi), cross-compilation requires"
echo "building on each target platform or using Docker with native toolchains."
echo ""
echo "To build for other platforms, run this script on:"
echo "  - Windows (AMD64 or 386)"
echo "  - macOS ARM64 (Apple Silicon)"
echo "  - macOS AMD64 (Intel Mac)"
