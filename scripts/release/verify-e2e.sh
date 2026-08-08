#!/bin/sh
set -eu

ARTIFACT=${1:-}
if [ -z "$ARTIFACT" ]; then
    echo "usage: scripts/release/verify-e2e.sh ARTIFACT_DIRECTORY" >&2
    exit 2
fi
for command in cmp jq shasum; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "required command is missing: $command" >&2
        exit 1
    fi
done
for file in \
    checksums.sha256 manifest.json ollama-models.json version.json health.json \
    codex-ingest.json assisted-job.json hybrid-recall.json assisted-recall.json \
    export-validation.json import-dry-run.json import.json imported-database-validation.json \
    restored-database-validation.json source-subject.canonical.json round-trip-subject.canonical.json \
    security-gates.txt; do
    if [ ! -f "$ARTIFACT/$file" ]; then
        echo "release E2E artifact is missing $file" >&2
        exit 1
    fi
done

(
    cd "$ARTIFACT"
    shasum -a 256 -c checksums.sha256
)
jq -e '.schema_version == 1 and .assertions.codex_ingest == "succeeded" and .assertions.assisted_formation == "succeeded" and .assertions.hybrid_recall and .assertions.portable_round_trip and .assertions.sqlite_backup_restore and .assertions.security_gates' "$ARTIFACT/manifest.json" >/dev/null
jq -e '.program == "memxplore" and .version == "0.1.0" and .protocol_version == "v1" and .storage_schema_version == 4 and .export_schema_version == 1' "$ARTIFACT/version.json" >/dev/null
jq -e '.status == "ok"' "$ARTIFACT/health.json" >/dev/null
jq -e '.accepted == 1 and .results[0].job.state == "succeeded"' "$ARTIFACT/codex-ingest.json" >/dev/null
jq -e '.state == "succeeded"' "$ARTIFACT/assisted-job.json" >/dev/null
jq -e '.mode == "hybrid" and (.fallback_reason // "") == "" and (.items | length) >= 1' "$ARTIFACT/hybrid-recall.json" >/dev/null
jq -e '.mode == "hybrid" and (.items | length) >= 1' "$ARTIFACT/assisted-recall.json" >/dev/null
jq -e '.valid and .kind == "subject_export" and .schema_version == 1' "$ARTIFACT/export-validation.json" >/dev/null
jq -e '.dry_run and .memories >= 2 and .versions >= 2' "$ARTIFACT/import-dry-run.json" >/dev/null
jq -e '(.dry_run | not) and .memories >= 2 and .versions >= 2' "$ARTIFACT/import.json" >/dev/null
jq -e '.valid and .kind == "sqlite"' "$ARTIFACT/imported-database-validation.json" >/dev/null
jq -e '.valid and .kind == "sqlite"' "$ARTIFACT/restored-database-validation.json" >/dev/null
cmp "$ARTIFACT/source-subject.canonical.json" "$ARTIFACT/round-trip-subject.canonical.json"
test "$(grep -c -- '--- PASS: TestSecurityGate' "$ARTIFACT/security-gates.txt")" -ge 4

echo "MemXplore release E2E artifact verified: $ARTIFACT"
