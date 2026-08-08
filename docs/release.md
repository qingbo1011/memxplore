# Release Verification

This runbook defines the v0.1.0 release procedure. Run every command from a clean checkout of the release revision with Go 1.26.x. Release scripts use isolated caches under `/tmp` unless `MEMXPLORE_RELEASE_GOCACHE` or `MEMXPLORE_RELEASE_GOMODCACHE` is set.

## Version and source checks

```sh
test "$(git branch --show-current)" = main
test -z "$(git status --porcelain)"
test "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)"
test "$(go env GOVERSION)" = go1.26.4
go run ./cmd/memxplore version --json
```

The source version command must report program `0.1.0`, protocol `v1`, storage schema `4`, and export schema `1` on the release commit.

## Automated gates

```sh
test -z "$(gofmt -l .)"
npx -y @redocly/cli@2.46.0 lint api/openapi.yaml --format stylish
GOCACHE=/tmp/memxplore-gocache GOMODCACHE=/tmp/memxplore-gomodcache go vet ./...
GOCACHE=/tmp/memxplore-gocache GOMODCACHE=/tmp/memxplore-gomodcache \
  go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...
GOCACHE=/tmp/memxplore-race-gocache GOMODCACHE=/tmp/memxplore-gomodcache \
  go test -race -coverprofile=/tmp/memxplore-v0.1.0-coverage.out ./...
GOCACHE=/tmp/memxplore-gocache GOMODCACHE=/tmp/memxplore-gomodcache \
  go test ./internal/strategy/formation ./internal/adapters/sqlite \
  -run '^TestSecurityGate' -v
```

Run and verify a fresh deterministic lifecycle artifact:

```sh
go run ./cmd/memxplore benchmark internal \
  --output runs \
  --run-id internal-lifecycle-v0.1.0 \
  --seed 1
go run ./cmd/memxplore eval verify \
  --run runs/internal-lifecycle-v0.1.0
```

CI must pass the same formatting, OpenAPI, vet, static-analysis, race, security, and deterministic-benchmark gates. Its cross-build matrix covers Darwin arm64, Linux amd64, Linux arm64, and Windows amd64.

## Local model E2E

The E2E requires the two exact models and digests listed in [provider configuration](providers.md) to be installed in a running loopback Ollama. The script validates their digests and never pulls a model.

```sh
scripts/release/e2e-local.sh runs/release-e2e-v0.1.0
scripts/release/verify-e2e.sh runs/release-e2e-v0.1.0
```

The artifact binds the source revision, Go version, OS/architecture, model identities, calls, responses, logs, SQLite backup, portable round-trip, checksums, and security results.

## Release assets

Generate and independently verify the four deterministic binary archives:

```sh
scripts/release/build-assets.sh v0.1.0 dist/v0.1.0-assets
scripts/release/verify-assets.sh dist/v0.1.0-assets v0.1.0
```

Generate the two evidence archives from previously verified immutable runs:

```sh
scripts/release/build-evidence.sh v0.1.0 dist/v0.1.0-evidence \
  runs/release-e2e-v0.1.0 \
  runs/internal-lifecycle-v0.1.0 \
  runs/longmemeval-v1-full-20260808-r2 \
  runs/longmemeval-v2-small-smoke-20260808 \
  runs/longmemeval-v1-local-answer-20260808-r2
scripts/release/verify-evidence.sh dist/v0.1.0-evidence v0.1.0
```

The evidence builder rejects symbolic links and unexpected evaluation files. Published evidence includes manifests, metrics, predictions, traces, reports, bounded local-model calls, and hashes. It excludes upstream datasets, model weights, cloud credentials, and restricted data.

Build binary and evidence outputs twice under different output directories and require `diff -qr` to produce no output before publication.

## Tag and publication

Push the release commit, wait for its GitHub Actions run to succeed, and then create an annotated tag:

```sh
git tag -a v0.1.0 -m "MemXplore v0.1.0"
test "$(git cat-file -t v0.1.0)" = tag
git push origin v0.1.0
```

Create the public GitHub Release from the tracked release notes and attach every file from both verified output directories:

```sh
gh release create v0.1.0 \
  --title "MemXplore v0.1.0" \
  --notes-file docs/releases/v0.1.0.md \
  dist/v0.1.0-assets/* \
  dist/v0.1.0-evidence/*
```

The release must not be a draft or prerelease. Verify the published tag target, release state, asset count, and checksums through the GitHub API before declaring completion.
