#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
VERSION=${1:-}
OUTPUT=${2:-}
if [ -z "$VERSION" ] || [ -z "$OUTPUT" ]; then
    echo "usage: scripts/release/build-assets.sh vSEMVER OUTPUT_DIRECTORY" >&2
    exit 2
fi
case "$VERSION" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *)
        echo "release version must be vSEMVER" >&2
        exit 2
        ;;
esac
case "$OUTPUT" in
    /*) ;;
    *) OUTPUT="$REPO_ROOT/$OUTPUT" ;;
esac
if [ -e "$OUTPUT" ]; then
    echo "release asset output already exists: $OUTPUT" >&2
    exit 1
fi
if [ -n "$(git -C "$REPO_ROOT" status --porcelain)" ]; then
    echo "release asset build requires a clean worktree" >&2
    exit 1
fi
for command in go git jq shasum; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "required command is missing: $command" >&2
        exit 1
    fi
done

cd "$REPO_ROOT"
FINAL_OUTPUT=$OUTPUT
OUTPUT="$FINAL_OUTPUT.partial.$$"
TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/memxplore-release-assets.XXXXXX")
PUBLISHED=0
cleanup() {
    rm -rf "$TMP_ROOT"
    if [ "$PUBLISHED" -eq 0 ]; then
        rm -rf "$OUTPUT"
    fi
}
trap cleanup EXIT INT TERM
mkdir -p "$OUTPUT"

PROGRAM_VERSION=${VERSION#v}
REVISION=$(git rev-parse HEAD)
BUILD_EPOCH=$(git show -s --format=%ct HEAD)
LDFLAGS="-s -w -X github.com/qingbo1011/memxplore/internal/buildinfo.Version=$PROGRAM_VERSION"
RELEASE_GOCACHE=${MEMXPLORE_RELEASE_GOCACHE:-/tmp/memxplore-release-gocache}
RELEASE_GOMODCACHE=${MEMXPLORE_RELEASE_GOMODCACHE:-/tmp/memxplore-gomodcache}

build_archive() {
    tier=$1
    goos=$2
    goarch=$3
    format=$4
    extension=
    binary=memxplore
    if [ "$goos" = windows ]; then
        binary=memxplore.exe
    fi
    if [ "$format" = zip ]; then
        extension=.zip
    else
        extension=.tar.gz
    fi
    root="memxplore_${PROGRAM_VERSION}_${goos}_${goarch}"
    stage="$TMP_ROOT/$root"
    mkdir -p "$stage"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        GOCACHE="$RELEASE_GOCACHE" \
        GOMODCACHE="$RELEASE_GOMODCACHE" \
        go build -trimpath -ldflags "$LDFLAGS" -o "$stage/$binary" ./cmd/memxplore
    chmod 0755 "$stage/$binary"
    cp LICENSE README.md "$stage/"
    GOCACHE="$RELEASE_GOCACHE" GOMODCACHE="$RELEASE_GOMODCACHE" \
        go run "$SCRIPT_DIR/package.go" \
        -input "$stage" \
        -output "$OUTPUT/$root$extension" \
        -format "$format" \
        -epoch "$BUILD_EPOCH"
    jq -n \
        --arg tier "$tier" \
        --arg goos "$goos" \
        --arg goarch "$goarch" \
        --arg artifact "$root$extension" \
        '{tier: $tier, goos: $goos, goarch: $goarch, cgo_enabled: false, artifact: $artifact}' \
        > "$TMP_ROOT/$goos-$goarch.json"
}

build_archive tier1 darwin arm64 tar.gz
build_archive tier1 linux amd64 tar.gz
build_archive tier2 linux arm64 tar.gz
build_archive tier2 windows amd64 zip

(
    cd "$OUTPUT"
    shasum -a 256 memxplore_* > SHA256SUMS
)
jq -s '.' \
    "$TMP_ROOT/darwin-arm64.json" \
    "$TMP_ROOT/linux-amd64.json" \
    "$TMP_ROOT/linux-arm64.json" \
    "$TMP_ROOT/windows-amd64.json" > "$TMP_ROOT/platforms.json"
GO_VERSION=$(go version)
jq -n \
    --arg version "$VERSION" \
    --arg revision "$REVISION" \
    --arg go_version "$GO_VERSION" \
    --argjson source_date_epoch "$BUILD_EPOCH" \
    --slurpfile platforms "$TMP_ROOT/platforms.json" '
    {
      schema_version: 1,
      version: $version,
      git_revision: $revision,
      go_version: $go_version,
      source_date_epoch: $source_date_epoch,
      platforms: $platforms[0],
      verification: {
        tier1: "cross-built; darwin/arm64 also runtime-E2E validated",
        tier2: "CGO-free cross-build validated"
      }
    }
' > "$OUTPUT/build-manifest.json"
cp "$SCRIPT_DIR/verify-assets.sh" "$OUTPUT/verify-assets.sh"
chmod 0755 "$OUTPUT/verify-assets.sh"

"$SCRIPT_DIR/verify-assets.sh" "$OUTPUT" "$VERSION"
mv "$OUTPUT" "$FINAL_OUTPUT"
PUBLISHED=1
echo "$FINAL_OUTPUT"
