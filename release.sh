#!/bin/bash
set -euo pipefail

# Configuration
APP_NAME="Converter_Blob_Teradocu"  # Using underscores for filename safety
OUTPUT_DIR="builds"
VERSION_FILE="version.go"
GIT_TAG_PREFIX="v"
DEFAULT_BUMP="patch"
MAIN_BRANCH="main"
DRY_RUN=false
SECURE_BUILD=true
ENV_DIR=".secure_env"

# Security configuration
export CGO_ENABLED=0
export GOEXPERIMENT=boringcrypto  # Go 1.21+ for FIPS crypto

# === Function: Get current version ===
get_old_version() {
    sed -nE 's/^const Version = "([0-9]+\.[0-9]+\.[0-9]+)"/\1/p' "$VERSION_FILE"
}

# === Function: Bump version ===
bump_version() {
    IFS='.' read -r major minor patch <<< "$1"
    case "$2" in
        major) echo "$((major + 1)).0.0" ;;
        minor) echo "$major.$((minor + 1)).0" ;;
        patch|*) echo "$major.$minor.$((patch + 1))" ;;
    esac
}

# === Function: Secure build process ===
secure_build() {
    local platforms=(
        "darwin/amd64"
        "darwin/arm64"
        "linux/amd64"
        "linux/arm64"
        "windows/amd64"
    )

    rm -rf "${OUTPUT_DIR}"
    mkdir -p "${OUTPUT_DIR}"
    
    # Create secure environment directory
    mkdir -p "${ENV_DIR}"
    chmod 700 "${ENV_DIR}"
    [[ -f ".env.example" && ! -f "${ENV_DIR}/.env" ]] && {
        cp ".env.example" "${ENV_DIR}/.env"
        chmod 600 "${ENV_DIR}/.env"
        echo "ℹ️  Created secure .env from example"
    }

    for platform in "${platforms[@]}"; do
        GOOS=${platform%/*}
        GOARCH=${platform#*/}
        output_name="${APP_NAME}_${GOOS}_${GOARCH}"
        
        if [[ "$GOOS" == "windows" ]]; then
            output_name+=".exe"
        fi

        echo "🔒 Building secure binary for ${GOOS}/${GOARCH}..."
        
        # Correct build command with proper output path
        GOOS=$GOOS GOARCH=$GOARCH go build \
            -trimpath \
            -ldflags "-w -s -extldflags=-static" \
            -buildvcs=false \
            -o "${OUTPUT_DIR}/${output_name}" \
            .
        
        # Enhanced macOS signing
        if [[ "$GOOS" == "darwin" ]]; then
            # Create entitlements file
            cat > "${OUTPUT_DIR}/entitlements.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>com.apple.security.cs.allow-unsigned-executable-memory</key>
    <true/>
</dict>
</plist>
EOF
            
            # Sign with hardened runtime
            codesign --force \
                    --options runtime \
                    --entitlements "${OUTPUT_DIR}/entitlements.plist" \
                    --sign "Developer ID Application: Your Name (TeamID)" \
                    "${OUTPUT_DIR}/${output_name}"
            
            # Verify the signature
            codesign -dv --verbose=4 "${OUTPUT_DIR}/${output_name}"
            spctl -a -t exec -vv "${OUTPUT_DIR}/${output_name}"
        fi
        
        # Create secure package with checksum
        (cd "${OUTPUT_DIR}" && \
         zip -r "${output_name}.zip" "${output_name}" "../${ENV_DIR}" && \
         shasum -a 256 "${output_name}.zip" > "${output_name}.zip.sha256")
    done
}

# === Main execution ===

# Branch check
current_branch=$(git rev-parse --abbrev-ref HEAD)
if [[ "$current_branch" != "$MAIN_BRANCH" && "$DRY_RUN" = false ]]; then
    echo "❌ Must be on '$MAIN_BRANCH' branch. Current: '$current_branch'"
    exit 1
fi

# Parse arguments
BUMP_TYPE="$DEFAULT_BUMP"
for arg in "$@"; do
    case "$arg" in
        --major) BUMP_TYPE="major" ;;
        --minor) BUMP_TYPE="minor" ;;
        --patch) BUMP_TYPE="patch" ;;
        --dry-run) DRY_RUN=true ;;
        --no-secure) SECURE_BUILD=false ;;
    esac
done

OLD_VERSION=$(get_old_version)
NEW_VERSION=$(bump_version "$OLD_VERSION" "$BUMP_TYPE")
TAG="${GIT_TAG_PREFIX}${NEW_VERSION}"

echo "🔄 Version bump: $OLD_VERSION → $NEW_VERSION"
echo "🏷️  Git tag to create: $TAG"
echo "📁 Output directory: $OUTPUT_DIR"
[[ "$SECURE_BUILD" == true ]] && echo "🔐 Secure build enabled"

if [[ "$DRY_RUN" = true ]]; then
    echo "🧪 DRY RUN MODE: No changes will be made"
    exit 0
fi

# Version update
sed -i '' "s/^const Version = \".*\"/const Version = \"$NEW_VERSION\"/" "$VERSION_FILE"

# Git operations
git add "$VERSION_FILE"
git commit -m "release: bump version to $NEW_VERSION"

# Remove existing tag if needed
if git rev-parse "$TAG" >/dev/null 2>&1; then
    echo "⚠️  Tag $TAG already exists. Removing..."
    git tag -d "$TAG"
    git push origin ":refs/tags/$TAG"
fi

# Tagging
echo "🏷️  Tagging: $TAG"
git tag "$TAG"
git push origin "$TAG"
git push

# Build process
if [[ "$SECURE_BUILD" == true ]]; then
    secure_build
else
    echo "🚀 Building for all platforms (standard mode)..."
    ./build_all.sh
fi

# Create release
echo "🚀 Creating GitHub release $TAG"
gh release create "$TAG" \
    "${OUTPUT_DIR}"/*.zip \
    "${OUTPUT_DIR}"/*.sha256 \
    --title "$TAG" \
    --notes "Release $TAG" \
    --verify-tag

REPO_NAME=$(git config --get remote.origin.url | sed -E 's/.*github.com[:\/](.*)\.git/\1/')
echo "✅ Release ready: https://github.com/$REPO_NAME/releases/tag/$TAG"
echo "🔒 SHA256 checksums:"
cat "${OUTPUT_DIR}"/*.sha256