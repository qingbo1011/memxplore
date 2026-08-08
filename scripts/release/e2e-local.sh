#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
OUTPUT=${1:-}
if [ -z "$OUTPUT" ]; then
    echo "usage: scripts/release/e2e-local.sh OUTPUT_DIRECTORY" >&2
    exit 2
fi
case "$OUTPUT" in
    /*) ;;
    *) OUTPUT="$REPO_ROOT/$OUTPUT" ;;
esac
if [ -e "$OUTPUT" ]; then
    echo "release E2E output already exists: $OUTPUT" >&2
    exit 1
fi
if [ -n "$(git -C "$REPO_ROOT" status --porcelain)" ]; then
    echo "release E2E requires a clean worktree" >&2
    exit 1
fi
for command in curl go git jq shasum; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "required command is missing: $command" >&2
        exit 1
    fi
done
cd "$REPO_ROOT"

PORT=${MEMXPLORE_E2E_PORT:-17878}
OLLAMA_URL=${MEMXPLORE_OLLAMA_URL:-http://127.0.0.1:11434}
case "$OLLAMA_URL" in
    http://127.0.0.1:*|http://localhost:*|http://\[::1\]:*) ;;
    *)
        echo "release E2E Ollama URL must use an explicit loopback HTTP address" >&2
        exit 1
        ;;
esac
EMBEDDING_MODEL=qwen3-embedding:0.6b
EMBEDDING_DIGEST=ac6da0dfba84a81fdbfbaf330198c33cd77c4cdfc53e8bc50eb581914a15621d
GENERATOR_MODEL=hf.co/HauhauCS/Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M
GENERATOR_DIGEST=f29a8c06f0c41f668c09bba4e9c7f2b3484ca59dcac2bfe8b7d5fa217e47ea1a
API_URL="http://127.0.0.1:$PORT"
TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/memxplore-release-e2e.XXXXXX")
FINAL_OUTPUT=$OUTPUT
OUTPUT="$FINAL_OUTPUT.partial.$$"
DAEMON_PID=
PUBLISHED=0

cleanup() {
    if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" >/dev/null 2>&1; then
        kill "$DAEMON_PID" >/dev/null 2>&1 || true
        wait "$DAEMON_PID" >/dev/null 2>&1 || true
    fi
    rm -rf "$TMP_ROOT"
    if [ "$PUBLISHED" -eq 0 ]; then
        rm -rf "$OUTPUT"
    fi
}
trap cleanup EXIT INT TERM

mkdir -p "$OUTPUT"
BIN="$TMP_ROOT/memxplore"
SOURCE_DB="$TMP_ROOT/source.sqlite"
TARGET_DB="$TMP_ROOT/target.sqlite"
RESTORED_DB="$TMP_ROOT/restored.sqlite"

curl -fsS "$OLLAMA_URL/api/tags" > "$TMP_ROOT/ollama-tags.json"
jq -e \
    --arg embedding "$EMBEDDING_MODEL" \
    --arg generator "$GENERATOR_MODEL" \
    --arg embedding_digest "$EMBEDDING_DIGEST" \
    --arg generator_digest "$GENERATOR_DIGEST" '
    def named($name): [.models[] | select(.name == $name or .model == $name)][0];
    {
      embedding: (named($embedding) | {name, model, digest, size}),
      generation: (named($generator) | {name, model, digest, size})
    }
    | select(.embedding.digest == $embedding_digest)
    | select(.generation.digest == $generator_digest)
' "$TMP_ROOT/ollama-tags.json" > "$OUTPUT/ollama-models.json"

REVISION=$(git -C "$REPO_ROOT" rev-parse HEAD)
GOCACHE=${GOCACHE:-/tmp/memxplore-gocache} \
GOMODCACHE=${GOMODCACHE:-/tmp/memxplore-gomodcache} \
go build -trimpath -ldflags "-s -w -X github.com/qingbo1011/memxplore/internal/buildinfo.Version=0.1.0" -o "$BIN" ./cmd/memxplore
"$BIN" version --json > "$OUTPUT/version.json"

cat > "$OUTPUT/codex-events.jsonl" <<'EOF'
{"id":"codex-e2e-message-1","type":"user_message","timestamp":"2026-08-08T12:00:00Z","thread_id":"thread-release-e2e","turn_id":"turn-1","role":"user","text":"The release owner prefers concise changelog entries with exact verification commands.","metadata":{"fixture":"release-e2e"}}
EOF

"$BIN" serve \
    --db "$SOURCE_DB" \
    --listen "127.0.0.1:$PORT" \
    --owner release-owner \
    --ollama-url "$OLLAMA_URL/v1" \
    --embedding-model "$EMBEDDING_MODEL" \
    --embedding-dimensions 1024 \
    --generator-model "$GENERATOR_MODEL" \
    --enable-assisted \
    --enable-agent-events > "$TMP_ROOT/daemon.log" 2>&1 &
DAEMON_PID=$!

attempt=0
until curl -fsS "$API_URL/v1/health" > "$OUTPUT/health.json" 2>/dev/null; do
    attempt=$((attempt + 1))
    if ! kill -0 "$DAEMON_PID" >/dev/null 2>&1; then
        cp "$TMP_ROOT/daemon.log" "$OUTPUT/daemon.log"
        echo "MemXplore daemon exited before becoming healthy" >&2
        exit 1
    fi
    if [ "$attempt" -ge 30 ]; then
        cp "$TMP_ROOT/daemon.log" "$OUTPUT/daemon.log"
        echo "MemXplore daemon did not become healthy" >&2
        exit 1
    fi
    sleep 1
done

"$BIN" ingest codex \
    --url "$API_URL" \
    --file "$OUTPUT/codex-events.jsonl" \
    --owner release-owner \
    --subject release-subject \
    --function factual \
    --strategy generator-free \
    --wait 30000 > "$OUTPUT/codex-ingest.json"
jq -e '.accepted == 1 and .results[0].job.state == "succeeded"' "$OUTPUT/codex-ingest.json" >/dev/null

"$BIN" remember \
    --url "$API_URL" \
    --owner release-owner \
    --subject release-subject \
    --context thread-release-e2e \
    --function factual \
    --strategy assisted \
    --source release-e2e \
    --idempotency-key release-assisted-1 \
    --text "The project release channel is named stable." \
    --wait 30000 > "$OUTPUT/assisted-remember.json"

ASSISTED_JOB_ID=$(jq -er '.job.id' "$OUTPUT/assisted-remember.json")
attempt=0
while :; do
    "$BIN" job --url "$API_URL" --id "$ASSISTED_JOB_ID" > "$OUTPUT/assisted-job.json"
    ASSISTED_STATE=$(jq -er '.state' "$OUTPUT/assisted-job.json")
    case "$ASSISTED_STATE" in
        succeeded) break ;;
        failed|canceled)
            echo "assisted formation ended in state $ASSISTED_STATE" >&2
            exit 1
            ;;
    esac
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 120 ]; then
        echo "assisted formation did not complete within 120 seconds" >&2
        exit 1
    fi
    sleep 1
done

"$BIN" recall \
    --url "$API_URL" \
    --owner release-owner \
    --subject release-subject \
    --context thread-release-e2e \
    --query "release owner concise changelog verification commands" \
    --mode hybrid \
    --token-budget 2048 \
    --candidate-limit 10 > "$OUTPUT/hybrid-recall.json"
jq -e '.mode == "hybrid" and (.fallback_reason // "") == "" and (.items | length) >= 1' "$OUTPUT/hybrid-recall.json" >/dev/null
jq -e '[.. | strings | select(contains("concise changelog entries"))] | length >= 1' "$OUTPUT/hybrid-recall.json" >/dev/null

"$BIN" recall \
    --url "$API_URL" \
    --owner release-owner \
    --subject release-subject \
    --context thread-release-e2e \
    --query "project release channel stable" \
    --mode hybrid \
    --token-budget 2048 \
    --candidate-limit 10 > "$OUTPUT/assisted-recall.json"
jq -e '.mode == "hybrid" and (.items | length) >= 1' "$OUTPUT/assisted-recall.json" >/dev/null
jq -e '[.. | strings | select(test("stable"; "i"))] | length >= 1' "$OUTPUT/assisted-recall.json" >/dev/null

"$BIN" data export \
    --db "$SOURCE_DB" \
    --namespace local \
    --principal local-cli \
    --owners release-owner \
    --subject release-subject \
    --output "$OUTPUT/source-subject.json" > "$OUTPUT/export.json"
"$BIN" data validate --input "$OUTPUT/source-subject.json" > "$OUTPUT/export-validation.json"
"$BIN" data import --db "$TARGET_DB" --input "$OUTPUT/source-subject.json" --dry-run > "$OUTPUT/import-dry-run.json"
"$BIN" data import --db "$TARGET_DB" --input "$OUTPUT/source-subject.json" > "$OUTPUT/import.json"
"$BIN" data validate --db "$TARGET_DB" > "$OUTPUT/imported-database-validation.json"
"$BIN" data export \
    --db "$TARGET_DB" \
    --namespace local \
    --principal local-cli \
    --owners release-owner \
    --subject release-subject \
    --output "$OUTPUT/round-trip-subject.json" > "$OUTPUT/round-trip-export.json"
jq -S 'del(.exported_at)' "$OUTPUT/source-subject.json" > "$OUTPUT/source-subject.canonical.json"
jq -S 'del(.exported_at)' "$OUTPUT/round-trip-subject.json" > "$OUTPUT/round-trip-subject.canonical.json"
cmp "$OUTPUT/source-subject.canonical.json" "$OUTPUT/round-trip-subject.canonical.json"

"$BIN" data backup --db "$SOURCE_DB" --output "$OUTPUT/source-backup.sqlite" > "$OUTPUT/backup.json"
"$BIN" data restore --backup "$OUTPUT/source-backup.sqlite" --db "$RESTORED_DB" > "$OUTPUT/restore.json"
"$BIN" data validate --db "$RESTORED_DB" > "$OUTPUT/restored-database-validation.json"

GOCACHE=${GOCACHE:-/tmp/memxplore-gocache} \
GOMODCACHE=${GOMODCACHE:-/tmp/memxplore-gomodcache} \
go test ./internal/strategy/formation ./internal/adapters/sqlite \
    -run '^TestSecurityGate' -v > "$OUTPUT/security-gates.txt"

cp "$TMP_ROOT/daemon.log" "$OUTPUT/daemon.log"
cp "$SCRIPT_DIR/e2e-local.sh" "$OUTPUT/reproduce.sh"
cp "$SCRIPT_DIR/verify-e2e.sh" "$OUTPUT/verify.sh"
RUN_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
GO_VERSION=$(go version)
GOOS=$(go env GOOS)
GOARCH=$(go env GOARCH)
jq -n \
    --arg run_at "$RUN_AT" \
    --arg revision "$REVISION" \
    --arg go_version "$GO_VERSION" \
    --arg goos "$GOOS" \
    --arg goarch "$GOARCH" \
    --arg ollama_url "$OLLAMA_URL" \
    --arg embedding_model "$EMBEDDING_MODEL" \
    --arg embedding_digest "$EMBEDDING_DIGEST" \
    --arg generator_model "$GENERATOR_MODEL" \
    --arg generator_digest "$GENERATOR_DIGEST" '
    {
      schema_version: 1,
      run_at: $run_at,
      git_revision: $revision,
      go_version: $go_version,
      platform: {os: $goos, arch: $goarch},
      ollama: {
        base_url: $ollama_url,
        embedding: {model: $embedding_model, digest: $embedding_digest, dimensions: 1024},
        generation: {model: $generator_model, digest: $generator_digest, think: false}
      },
      assertions: {
        codex_ingest: "succeeded",
        assisted_formation: "succeeded",
        hybrid_recall: true,
        portable_round_trip: true,
        sqlite_backup_restore: true,
        security_gates: true
      }
    }
' > "$OUTPUT/manifest.json"

(
    cd "$OUTPUT"
    find . -type f ! -name checksums.sha256 -print | LC_ALL=C sort | while IFS= read -r file; do
        shasum -a 256 "$file"
    done | sed 's#  \./#  #'
) > "$OUTPUT/checksums.sha256"

"$SCRIPT_DIR/verify-e2e.sh" "$OUTPUT"
mv "$OUTPUT" "$FINAL_OUTPUT"
PUBLISHED=1
echo "$FINAL_OUTPUT"
