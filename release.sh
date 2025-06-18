#!/bin/bash
set -e

APP_NAME="Converter Blob Teradocu"
OUTPUT_DIR="builds"
VERSION_FILE="version.go"
BUILD_SCRIPT="./build_all.sh"
GIT_TAG_PREFIX="v"
DEFAULT_BUMP="patch"
MAIN_BRANCH="main"
DRY_RUN=false

# === Fungsi: Ambil versi lama dari version.go ===
get_old_version() {
    sed -nE 's/^const Version = "([0-9]+\.[0-9]+\.[0-9]+)"/\1/p' "$VERSION_FILE"
}

# === Fungsi: Bump versi ===
bump_version() {
    IFS='.' read -r major minor patch <<< "$1"
    case "$2" in
        major) echo "$((major + 1)).0.0" ;;
        minor) echo "$major.$((minor + 1)).0" ;;
        patch|*) echo "$major.$minor.$((patch + 1))" ;;
    esac
}

# === Cek branch ===
current_branch=$(git rev-parse --abbrev-ref HEAD)
if [[ "$current_branch" != "$MAIN_BRANCH" && "$DRY_RUN" = false ]]; then
    echo "❌ Harus berada di branch '$MAIN_BRANCH'. Sekarang: '$current_branch'"
    exit 1
fi

# === Parse argumen ===
BUMP_TYPE="$DEFAULT_BUMP"
for arg in "$@"; do
    case "$arg" in
        --major) BUMP_TYPE="major" ;;
        --minor) BUMP_TYPE="minor" ;;
        --patch) BUMP_TYPE="patch" ;;
        --dry-run) DRY_RUN=true ;;
    esac
done

OLD_VERSION=$(get_old_version)
NEW_VERSION=$(bump_version "$OLD_VERSION" "$BUMP_TYPE")
TAG="${GIT_TAG_PREFIX}${NEW_VERSION}"

echo "🔄 Version bump: $OLD_VERSION → $NEW_VERSION"
echo "🏷️  Git tag to create: $TAG"
echo "📁 Output directory: $OUTPUT_DIR"

if [[ "$DRY_RUN" = true ]]; then
    echo "🧪 DRY RUN MODE: tidak akan ada file yang diubah atau dipush."
    exit 0
fi

# Update version.go
sed -i '' "s/^const Version = \".*\"/const Version = \"$NEW_VERSION\"/" "$VERSION_FILE"

# Commit version bump
git add "$VERSION_FILE"
git commit -m "release: bump version to $NEW_VERSION"

# Remove existing tag if exists
if git rev-parse "$TAG" >/dev/null 2>&1; then
    echo "⚠️  Tag $TAG sudah ada. Menghapus dulu..."
    git tag -d "$TAG"
    git push origin ":refs/tags/$TAG"
fi

# Tagging
echo "🏷️  Tagging: $TAG"
git tag "$TAG"
git push origin "$TAG"
git push

# Build dan Zip
echo "🚀 Building for all platforms..."
$BUILD_SCRIPT

echo "📦 Zipping binaries..."
cd "$OUTPUT_DIR"
for file in *; do
    zip -r "${file}.zip" "$file"
done
cd ..

# GitHub Release
echo "🚀 Creating GitHub release $TAG"
gh release create "$TAG" "$OUTPUT_DIR"/*.zip --title "$TAG" --notes "Release $TAG"

REPO_NAME=$(git config --get remote.origin.url | sed -E 's/.*github.com[:\/](.*)\.git/\1/')
echo "✅ Done: https://github.com/$REPO_NAME/releases/tag/$TAG"
