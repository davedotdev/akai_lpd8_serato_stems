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
if [ "$CURRENT_OS" = "windows" ]; then
    EXT=".exe"
else
    EXT=""
fi

# On macOS, build for both arm64 and amd64
if [ "$CURRENT_OS" = "darwin" ]; then
    echo "Building for darwin/arm64..."
    CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CGO_CFLAGS="-arch arm64" CGO_LDFLAGS="-arch arm64" \
        go build -ldflags "-X main.Version=$VERSION" -o "$OUTPUT_DIR/${APP_NAME}-darwin-arm64" .

    echo "Building for darwin/amd64..."
    CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CGO_CFLAGS="-arch x86_64" CGO_LDFLAGS="-arch x86_64" \
        go build -ldflags "-X main.Version=$VERSION" -o "$OUTPUT_DIR/${APP_NAME}-darwin-amd64" .
else
    echo "Building for $CURRENT_OS/$CURRENT_ARCH..."
    go build -ldflags "-X main.Version=$VERSION" -o "$OUTPUT_DIR/${APP_NAME}-${CURRENT_OS}-${CURRENT_ARCH}${EXT}" .
fi

# Generate default config (use current architecture binary)
echo "Generating default config..."
if [ "$CURRENT_OS" = "darwin" ]; then
    "$OUTPUT_DIR/${APP_NAME}-darwin-${CURRENT_ARCH}" -genconfig "$OUTPUT_DIR/config.json"
else
    "$OUTPUT_DIR/${APP_NAME}-${CURRENT_OS}-${CURRENT_ARCH}${EXT}" -genconfig "$OUTPUT_DIR/config.json"
fi

echo ""
echo "Build complete! Files in $OUTPUT_DIR/:"
ls -la "$OUTPUT_DIR/"

echo ""
echo "Note: Due to CGO dependencies (rtmidi), cross-compilation requires"
echo "building on each target platform or using Docker with native toolchains."
echo ""
echo "macOS builds both arm64 and amd64 automatically."
echo "To build for Windows, run this script on a Windows machine."
