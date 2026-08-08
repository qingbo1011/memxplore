#!/bin/sh
set -eu

EVIDENCE=${1:-}
VERSION=${2:-}
if [ -z "$EVIDENCE" ] || [ -z "$VERSION" ]; then
    echo "usage: scripts/release/verify-evidence.sh EVIDENCE_DIRECTORY vSEMVER" >&2
    exit 2
fi
PROGRAM_VERSION=${VERSION#v}
for command in grep jq shasum tar; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "required command is missing: $command" >&2
        exit 1
    fi
done
ARCHIVE_LIST=$(mktemp "${TMPDIR:-/tmp}/memxplore-evidence-list.XXXXXX")
cleanup() {
    rm -f "$ARCHIVE_LIST"
}
trap cleanup EXIT INT TERM

(
    cd "$EVIDENCE"
    shasum -a 256 -c EVIDENCE_SHA256SUMS
)
jq -e \
    --arg version "$VERSION" '
    .schema_version == 1
    and .version == $version
    and (.git_revision | length) == 40
    and .local_e2e.assertions.hybrid_recall
    and .local_e2e.assertions.portable_round_trip
    and .local_e2e.assertions.sqlite_backup_restore
    and .local_e2e.assertions.security_gates
    and (.evaluation | length) == 4
    and ([.evaluation[].manifest.benchmark] | sort) == [
      "internal-lifecycle-v1",
      "longmemeval-v1-local-answer",
      "longmemeval-v1-retrieval",
      "longmemeval-v2-small-adapter-smoke"
    ]
' "$EVIDENCE/evidence-manifest.json" >/dev/null

E2E_ARCHIVE="$EVIDENCE/memxplore_${PROGRAM_VERSION}_local_e2e_evidence.tar.gz"
E2E_ROOT="memxplore_${PROGRAM_VERSION}_local_e2e_evidence"
tar -tzf "$E2E_ARCHIVE" > "$ARCHIVE_LIST"
grep -qx "$E2E_ROOT/manifest.json" "$ARCHIVE_LIST"
grep -qx "$E2E_ROOT/checksums.sha256" "$ARCHIVE_LIST"
grep -qx "$E2E_ROOT/verify.sh" "$ARCHIVE_LIST"

EVALUATION_ARCHIVE="$EVIDENCE/memxplore_${PROGRAM_VERSION}_evaluation_evidence.tar.gz"
EVALUATION_ROOT="memxplore_${PROGRAM_VERSION}_evaluation_evidence"
tar -tzf "$EVALUATION_ARCHIVE" > "$ARCHIVE_LIST"
for run in \
    ci-internal-lifecycle \
    longmemeval-v1-full-20260808-r2 \
    longmemeval-v2-small-smoke-20260808 \
    longmemeval-v1-local-answer-20260808-r2; do
    grep -qx "$EVALUATION_ROOT/$run/manifest.json" "$ARCHIVE_LIST"
    grep -qx "$EVALUATION_ROOT/$run/metrics.json" "$ARCHIVE_LIST"
done

echo "MemXplore release evidence verified: $EVIDENCE"
