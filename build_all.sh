#!/bin/bash

set -e

APP_NAME="Converter Blob Teradocu" # Ubah ke nama aplikasi kamu
OUTPUT_DIR="builds"
VERSION=$(sed -nE 's/^const Version = "([0-9]+\.[0-9]+\.[0-9]+)"/\1/p' version.go)

mkdir -p "$OUTPUT_DIR"

platforms=(
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
  "windows/arm64"
  "darwin/amd64"
  "darwin/arm64"
)

echo "🚀 Building version $VERSION for all platforms..."

for platform in "${platforms[@]}"; do
    IFS="/" read -r GOOS GOARCH <<< "$platform"
    output_name="${APP_NAME}_${VERSION}_${GOOS}_${GOARCH}"
    
    if [ "$GOOS" = "windows" ]; then
        output_name+=".exe"
    fi

    echo "🔧 Building $GOOS/$GOARCH..."
    env GOOS=$GOOS GOARCH=$GOARCH go build -o "$OUTPUT_DIR/$output_name" .

    if [ $? -ne 0 ]; then
        echo "❌ Build failed for $platform"
        exit 1
    fi
done

echo "✅ All binaries built in: $OUTPUT_DIR/"
