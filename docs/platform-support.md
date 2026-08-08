# Platform Support

MemXplore v0.1.0 is a single CGO-free Go binary. Platform tiers describe the evidence available for the release, not a promise that every external Ollama build or operating-system configuration behaves identically.

| Tier | Platform | v0.1.0 evidence |
| --- | --- | --- |
| Tier 1 | Linux amd64 | Full unit/race/static-analysis/benchmark CI plus native build |
| Tier 1 | Darwin arm64 | Full local test gates and Codex + Ollama runtime E2E plus native build |
| Tier 2 | Linux arm64 | CI and local CGO-free cross-build |
| Tier 2 | Windows amd64 | CI and local CGO-free cross-build |

Tier 1 means the MemXplore binary runs release-level behavioral checks on that platform/architecture. Tier 2 means the exact source cross-builds with `CGO_ENABLED=0`; it does not claim that the release E2E was executed there.

## Runtime requirements

- Go is not required when using a release binary. Building from source requires Go 1.26.x.
- SQLite is embedded through `modernc.org/sqlite`; no system SQLite or C toolchain is required.
- The generator-free lexical baseline requires no model server.
- Semantic/hybrid recall and assisted formation require an explicitly configured compatible provider. The v0.1.0 release E2E uses the exact local Ollama models and digests in [provider configuration](providers.md).
- Non-loopback HTTP deployment requires a scoped token and external TLS termination.

## Reproducing release assets

From a clean checkout of the release revision:

```sh
scripts/release/build-assets.sh v0.1.0 dist/v0.1.0
scripts/release/verify-assets.sh dist/v0.1.0 v0.1.0
```

The builder sets `CGO_ENABLED=0`, `-trimpath`, the release version through Go ldflags, and a source-revision timestamp for deterministic archive metadata. It writes four archives, `SHA256SUMS`, and a machine-readable `build-manifest.json`. Every archive contains the binary, `LICENSE`, and `README.md`.
