#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
MIRROR=${1:-$REPO_ROOT/docs.zh-CN}
case "$MIRROR" in
    /*) ;;
    *) MIRROR="$REPO_ROOT/$MIRROR" ;;
esac

for command in awk git mktemp sed; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "required command is missing: $command" >&2
        exit 1
    fi
done
if command -v shasum >/dev/null 2>&1; then
    source_digest() {
        shasum -a 256 "$1" | awk '{print $1}'
    }
elif command -v sha256sum >/dev/null 2>&1; then
    source_digest() {
        sha256sum "$1" | awk '{print $1}'
    }
else
    echo "required command is missing: shasum or sha256sum" >&2
    exit 1
fi

SOURCE_LIST=$(mktemp "${TMPDIR:-/tmp}/memxplore-zh-docs.XXXXXX")
cleanup() {
    rm -f "$SOURCE_LIST"
}
trap cleanup EXIT INT TERM
git -C "$REPO_ROOT" ls-files '*.md' > "$SOURCE_LIST"

total=0
invalid=0
while IFS= read -r source; do
    [ -n "$source" ] || continue
    total=$((total + 1))
    target="$MIRROR/$source"
    if [ ! -f "$target" ]; then
        echo "missing translation: $source" >&2
        invalid=$((invalid + 1))
        continue
    fi
    recorded_source=$(sed -n '1s/^<!-- source: \(.*\) -->$/\1/p' "$target")
    recorded_digest=$(sed -n '2s/^<!-- source-sha256: \([0-9a-f][0-9a-f]*\) -->$/\1/p' "$target")
    actual_digest=$(source_digest "$REPO_ROOT/$source")
    if [ "$recorded_source" != "$source" ]; then
        echo "source marker mismatch: $source" >&2
        invalid=$((invalid + 1))
    elif [ "$recorded_digest" != "$actual_digest" ]; then
        echo "stale translation: $source" >&2
        invalid=$((invalid + 1))
    fi
done < "$SOURCE_LIST"

if [ "$invalid" -ne 0 ]; then
    echo "Chinese documentation mirror has $invalid invalid file(s) out of $total" >&2
    exit 1
fi
echo "Chinese documentation mirror is current: $total/$total files"
