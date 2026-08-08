#!/bin/sh
set -eu

ASSETS=${1:-}
VERSION=${2:-}
if [ -z "$ASSETS" ] || [ -z "$VERSION" ]; then
    echo "usage: scripts/release/verify-assets.sh ASSET_DIRECTORY vSEMVER" >&2
    exit 2
fi
PROGRAM_VERSION=${VERSION#v}
for command in jq shasum tar unzip; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "required command is missing: $command" >&2
        exit 1
    fi
done
ARCHIVE_LIST=$(mktemp "${TMPDIR:-/tmp}/memxplore-assets-list.XXXXXX")
cleanup() {
    rm -f "$ARCHIVE_LIST"
}
trap cleanup EXIT INT TERM

(
    cd "$ASSETS"
    shasum -a 256 -c SHA256SUMS
)
jq -e \
    --arg version "$VERSION" '
    .schema_version == 1
    and .version == $version
    and (.git_revision | length) == 40
    and (.platforms | length) == 4
    and ([.platforms[].tier] | sort) == ["tier1", "tier1", "tier2", "tier2"]
    and ([.platforms[] | select(.cgo_enabled == false)] | length) == 4
' "$ASSETS/build-manifest.json" >/dev/null

for platform in darwin_arm64 linux_amd64 linux_arm64; do
    archive="$ASSETS/memxplore_${PROGRAM_VERSION}_${platform}.tar.gz"
    root="memxplore_${PROGRAM_VERSION}_${platform}"
    tar -tzf "$archive" | sort > "$ARCHIVE_LIST"
    test "$(wc -l < "$ARCHIVE_LIST" | tr -d ' ')" -eq 3
    grep -qx "$root/LICENSE" "$ARCHIVE_LIST"
    grep -qx "$root/README.md" "$ARCHIVE_LIST"
    grep -qx "$root/memxplore" "$ARCHIVE_LIST"
done
windows_archive="$ASSETS/memxplore_${PROGRAM_VERSION}_windows_amd64.zip"
windows_root="memxplore_${PROGRAM_VERSION}_windows_amd64"
unzip -Z1 "$windows_archive" | sort > "$ARCHIVE_LIST"
test "$(wc -l < "$ARCHIVE_LIST" | tr -d ' ')" -eq 3
grep -qx "$windows_root/LICENSE" "$ARCHIVE_LIST"
grep -qx "$windows_root/README.md" "$ARCHIVE_LIST"
grep -qx "$windows_root/memxplore.exe" "$ARCHIVE_LIST"

echo "MemXplore release assets verified: $ASSETS"
