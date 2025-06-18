#!/bin/bash
set -e

APP_NAME="Converter Blob Teradocu"
OUTPUT_DIR="builds"
VERSION=$(sed -nE 's/^const Version = "([0-9]+\.[0-9]+\.[0-9]+)"/\1/p' version.go)
TAG="v$VERSION"

echo "🏷️  Tagging: $TAG"
git tag "$TAG"
git push origin "$TAG"

echo "🚀 Building for all platforms..."
./build_all.sh

echo "📦 Zipping binaries..."
cd "$OUTPUT_DIR"
for file in *; do
    zip "${file}.zip" "$file"
done
cd ..

echo "🚀 Creating GitHub release $TAG"
gh release create "$TAG" "$OUTPUT_DIR"/*.zip --title "$TAG" --notes "Release $TAG"

echo "✅ Done: https://github.com/imamdwicaksono/coverter_blob/releases/tag/$TAG"
