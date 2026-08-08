#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
VERSION=${1:-}
OUTPUT=${2:-}
E2E=${3:-}
INTERNAL=${4:-}
LONGMEMEVAL_V1=${5:-}
LONGMEMEVAL_V2=${6:-}
LOCAL_ANSWER=${7:-}
if [ -z "$VERSION" ] || [ -z "$OUTPUT" ] || [ -z "$E2E" ] || [ -z "$INTERNAL" ] || \
    [ -z "$LONGMEMEVAL_V1" ] || [ -z "$LONGMEMEVAL_V2" ] || [ -z "$LOCAL_ANSWER" ]; then
    echo "usage: scripts/release/build-evidence.sh vSEMVER OUTPUT E2E INTERNAL LONGMEMEVAL_V1 LONGMEMEVAL_V2 LOCAL_ANSWER" >&2
    exit 2
fi
case "$VERSION" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *)
        echo "release version must be vSEMVER" >&2
        exit 2
        ;;
esac

resolve_path() {
    case "$1" in
        /*) printf '%s\n' "$1" ;;
        *) printf '%s/%s\n' "$REPO_ROOT" "$1" ;;
    esac
}

OUTPUT=$(resolve_path "$OUTPUT")
E2E=$(resolve_path "$E2E")
INTERNAL=$(resolve_path "$INTERNAL")
LONGMEMEVAL_V1=$(resolve_path "$LONGMEMEVAL_V1")
LONGMEMEVAL_V2=$(resolve_path "$LONGMEMEVAL_V2")
LOCAL_ANSWER=$(resolve_path "$LOCAL_ANSWER")
if [ -e "$OUTPUT" ]; then
    echo "release evidence output already exists: $OUTPUT" >&2
    exit 1
fi
if [ -n "$(git -C "$REPO_ROOT" status --porcelain)" ]; then
    echo "release evidence build requires a clean worktree" >&2
    exit 1
fi
for command in cp find go git jq shasum; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "required command is missing: $command" >&2
        exit 1
    fi
done
for directory in "$E2E" "$INTERNAL" "$LONGMEMEVAL_V1" "$LONGMEMEVAL_V2" "$LOCAL_ANSWER"; do
    if [ ! -d "$directory" ]; then
        echo "evidence directory does not exist: $directory" >&2
        exit 1
    fi
    if [ -n "$(find "$directory" -type l -print -quit)" ]; then
        echo "evidence directory contains a symbolic link: $directory" >&2
        exit 1
    fi
done

cd "$REPO_ROOT"
FINAL_OUTPUT=$OUTPUT
OUTPUT="$FINAL_OUTPUT.partial.$$"
TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/memxplore-release-evidence.XXXXXX")
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
RELEASE_GOCACHE=${MEMXPLORE_RELEASE_GOCACHE:-/tmp/memxplore-release-gocache}
RELEASE_GOMODCACHE=${MEMXPLORE_RELEASE_GOMODCACHE:-/tmp/memxplore-gomodcache}

"$SCRIPT_DIR/verify-e2e.sh" "$E2E"
for run in "$INTERNAL" "$LONGMEMEVAL_V1" "$LONGMEMEVAL_V2" "$LOCAL_ANSWER"; do
    for file in manifest.json metrics.json predictions.jsonl report.html traces.jsonl; do
        if [ ! -f "$run/$file" ]; then
            echo "evaluation evidence is missing $file: $run" >&2
            exit 1
        fi
    done
    if [ "$(find "$run" -mindepth 1 -maxdepth 1 -type f | wc -l | tr -d ' ')" -ne 5 ]; then
        echo "evaluation evidence contains unexpected top-level files: $run" >&2
        exit 1
    fi
    if [ -n "$(find "$run" -mindepth 1 -type d -print -quit)" ]; then
        echo "evaluation evidence contains an unexpected directory: $run" >&2
        exit 1
    fi
    GOCACHE="$RELEASE_GOCACHE" GOMODCACHE="$RELEASE_GOMODCACHE" \
        go run ./cmd/memxplore eval verify --run "$run"
done

E2E_ROOT="$TMP_ROOT/memxplore_${PROGRAM_VERSION}_local_e2e_evidence"
EVALUATION_ROOT="$TMP_ROOT/memxplore_${PROGRAM_VERSION}_evaluation_evidence"
mkdir -p "$E2E_ROOT" "$EVALUATION_ROOT"
cp -R "$E2E/." "$E2E_ROOT/"
cp -R "$INTERNAL" "$EVALUATION_ROOT/ci-internal-lifecycle"
cp -R "$LONGMEMEVAL_V1" "$EVALUATION_ROOT/longmemeval-v1-full-20260808-r2"
cp -R "$LONGMEMEVAL_V2" "$EVALUATION_ROOT/longmemeval-v2-small-smoke-20260808"
cp -R "$LOCAL_ANSWER" "$EVALUATION_ROOT/longmemeval-v1-local-answer-20260808-r2"

GOCACHE="$RELEASE_GOCACHE" GOMODCACHE="$RELEASE_GOMODCACHE" \
    go run "$SCRIPT_DIR/package.go" \
    -input "$E2E_ROOT" \
    -output "$OUTPUT/memxplore_${PROGRAM_VERSION}_local_e2e_evidence.tar.gz" \
    -format tar.gz \
    -epoch "$BUILD_EPOCH"
GOCACHE="$RELEASE_GOCACHE" GOMODCACHE="$RELEASE_GOMODCACHE" \
    go run "$SCRIPT_DIR/package.go" \
    -input "$EVALUATION_ROOT" \
    -output "$OUTPUT/memxplore_${PROGRAM_VERSION}_evaluation_evidence.tar.gz" \
    -format tar.gz \
    -epoch "$BUILD_EPOCH"

jq -n \
    --arg version "$VERSION" \
    --arg revision "$REVISION" \
    --argjson source_date_epoch "$BUILD_EPOCH" \
    --slurpfile e2e "$E2E/manifest.json" \
    --slurpfile internal_manifest "$INTERNAL/manifest.json" \
    --slurpfile internal_metrics "$INTERNAL/metrics.json" \
    --slurpfile v1_manifest "$LONGMEMEVAL_V1/manifest.json" \
    --slurpfile v1_metrics "$LONGMEMEVAL_V1/metrics.json" \
    --slurpfile v2_manifest "$LONGMEMEVAL_V2/manifest.json" \
    --slurpfile v2_metrics "$LONGMEMEVAL_V2/metrics.json" \
    --slurpfile answer_manifest "$LOCAL_ANSWER/manifest.json" \
    --slurpfile answer_metrics "$LOCAL_ANSWER/metrics.json" '
    {
      schema_version: 1,
      version: $version,
      git_revision: $revision,
      source_date_epoch: $source_date_epoch,
      local_e2e: $e2e[0],
      evaluation: [
        {manifest: $internal_manifest[0], metrics: $internal_metrics[0]},
        {manifest: $v1_manifest[0], metrics: $v1_metrics[0]},
        {manifest: $v2_manifest[0], metrics: $v2_metrics[0]},
        {manifest: $answer_manifest[0], metrics: $answer_metrics[0]}
      ],
      exclusions: ["upstream datasets", "model weights", "credentials"]
    }
' > "$OUTPUT/evidence-manifest.json"
cp "$SCRIPT_DIR/verify-evidence.sh" "$OUTPUT/verify-evidence.sh"
chmod 0755 "$OUTPUT/verify-evidence.sh"
(
    cd "$OUTPUT"
    shasum -a 256 evidence-manifest.json memxplore_*.tar.gz > EVIDENCE_SHA256SUMS
)

"$SCRIPT_DIR/verify-evidence.sh" "$OUTPUT" "$VERSION"
mv "$OUTPUT" "$FINAL_OUTPUT"
PUBLISHED=1
echo "$FINAL_OUTPUT"
