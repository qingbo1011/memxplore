# Project Reading Guide

[简体中文](reading-guide.zh-CN.md)

This guide is for readers who want to understand the implementation, not only run the binary. MemXplore is organized around research questions and explicit contracts, so the shortest path through the repository is to follow one memory from evidence capture to recall before reading every package.

## Choose a route

| Goal | Suggested time | Read in this order |
| --- | --- | --- |
| Understand the project boundary | 15 minutes | [`README.md`](../README.md), [`CHARTER.md`](CHARTER.md), [ADR 0001](architecture/adr/0001-v0.1-scope-and-ports-adapters.md), [`ROADMAP.md`](ROADMAP.md) |
| Follow the memory lifecycle | 45 minutes | `internal/domain`, `internal/application/formation_job.go`, `internal/daemon/formation.go`, `internal/application/lifecycle.go` |
| Understand retrieval | 30 minutes | [`retrieval.md`](retrieval.md), `internal/application/retrieval.go`, `internal/adapters/sqlite/retrieval.go` |
| Integrate an Agent | 30 minutes | [`api.md`](api.md), [`api/openapi.yaml`](../api/openapi.yaml), `internal/api`, `sdk`, `internal/agentevent` |
| Reproduce an evaluation | 45 minutes | [`evaluation.md`](evaluation.md), `internal/evaluation`, `research/catalog.json` |
| Audit storage and security | 45 minutes | [`storage-safety.md`](storage-safety.md), `internal/adapters/sqlite`, `internal/auth`, [`SECURITY.md`](../SECURITY.md) |

## Keep this model in mind

The main write and read paths are:

```text
evidence
  -> Observation + durable formation Job
  -> typed Proposal
  -> apply policy
  -> Memory + immutable MemoryVersion + Operation
  -> lexical and optional embedding indexes
  -> RecallBundle + RetrievalTrace
```

The objects have deliberately different roles:

| Object | Meaning | Start here |
| --- | --- | --- |
| `Observation` | Immutable source evidence captured from a user, tool, or Agent event | `internal/domain/observation.go` |
| `Memory` | Stable identity, function, scope, and lifecycle state | `internal/domain/memory.go` |
| `MemoryVersion` | Immutable factual, experiential, or working payload with provenance and bitemporal metadata | `internal/domain/memory.go` |
| `Proposal` | A typed candidate change that must pass policy before persistence | `internal/application/proposal.go` |
| `Operation` | Audit record of an observed, proposed, or applied lifecycle action | `internal/domain/trace.go` |
| `RecallBundle` | Structured evidence with scores, citations, conflicts, budget use, and trace identity | `internal/application/retrieval.go` |
| `Strategy Package` | Versioned algorithm identity binding parameters, capabilities, fidelity, and source claims | `internal/strategy/package.go` |

Two boundaries prevent common reading mistakes. An Observation is evidence, not yet memory. A RecallBundle is evidence returned to an Agent, not a generated answer.

## Read the implementation in order

### 1. Establish scope and architecture

Read the [project charter](CHARTER.md) and the three [architecture decisions](architecture/adr/README.md) first. They explain why v0.1.0 implements explicit token-level factual, experiential, and working memory; why domain and application packages do not depend on transports or SQLite; and why most strategies are reference implementations rather than paper reproductions.

Then scan `cmd/memxplore/main.go`. Its `runContext` function lists every command, while `buildRuntime` is the composition root that wires SQLite, providers, retrieval, the formation worker, authentication, telemetry, and the HTTP server.

### 2. Learn the domain vocabulary

Read `internal/domain` before persistence code. A useful order is:

1. `scope.go` for namespace, owner, subject, actor, context, and visibility.
2. `content.go` and `taxonomy.go` for typed content and extensible research metadata.
3. `observation.go` and `memory.go` for source evidence, stable identities, and immutable versions.
4. `factual.go`, `experiential.go`, and `working.go` for function-specific payloads.
5. `trace.go` for lifecycle and retrieval evidence.

The `Validate` methods are executable documentation. Their tests in `internal/domain/domain_test.go` show which states and combinations are intentionally rejected.

### 3. Follow a `remember` request

Trace this path:

1. `internal/api/server.go` validates authorization and request limits in `remember` and `prepareRemember`.
2. The Observation and formation job are committed together through the SQLite job store.
3. `internal/daemon/formation.go` claims the durable job and chooses a generator-free or assisted formation strategy.
4. `internal/strategy/formation` produces a typed proposal.
5. `internal/application/lifecycle.go` validates the proposal, asks the apply policy, and records the result through the repository port.
6. `internal/adapters/sqlite/lifecycle.go` persists the Memory, immutable version, provenance, and operation atomically.
7. When configured, the formation worker writes an embedding for later semantic retrieval.

The job lease and retry behavior lives in `internal/application/jobs.go` and `internal/adapters/sqlite/jobs.go`. This is the place to read if you are studying idempotency or crash recovery.

### 4. Follow a `recall` request

Start with the contract in [`retrieval.md`](retrieval.md), then read `Retriever.Recall` in `internal/application/retrieval.go`.

The repository applies scope, visibility, lifecycle, function, valid-time, and system-time filters before ranking. Lexical candidates come from SQLite FTS5/BM25. Semantic candidates use a configured `EmbeddingProvider` and exact cosine scoring. Hybrid mode fuses both rankings with reciprocal rank fusion, then deduplicates, groups conflicts, applies the token budget, records score explanations, and writes a retrieval trace.

The SQL and FTS query handling are in `internal/adapters/sqlite/retrieval.go`. Retrieval behavior is easier to understand from `internal/application/retrieval_test.go` first, then `internal/adapters/sqlite/retrieval_test.go`.

### 5. Compare the interfaces

All public interfaces map onto the same application contracts:

- `cmd/memxplore/main.go` implements the CLI and local process composition.
- `internal/api/server.go` implements REST, authentication, request bounds, and lifecycle routes.
- `internal/api/mcp.go` implements stdio and Streamable HTTP MCP with a selected tool surface.
- `api/openapi.yaml` is the REST source of truth.
- `sdk` is the public Go client and must not expose internal application types.
- `internal/agentevent` validates the vendor-neutral AgentEvent v1 envelope and the explicit Codex JSONL adapter.

Use `internal/api/server_test.go`, `internal/api/mcp_test.go`, `sdk/client_test.go`, and `cmd/memxplore/main_test.go` as contract examples.

### 6. Study persistence and lifecycle safety

`internal/adapters/sqlite/migrations` shows the storage schema in order. `store.go` configures SQLite, `backup.go` protects migration and restore operations, `portable.go` implements subject export and import, and `purge.go` handles irreversible transitive deletion.

Read [`lifecycle.md`](lifecycle.md) beside `internal/application/lifecycle.go` and `internal/adapters/sqlite/lifecycle.go`. Archive, forget, and purge are separate operations. Purge is never automatic and must leave a non-content receipt rather than recoverable memory content.

### 7. Finish with evaluation

`internal/evaluation/internal.go` contains the smallest complete examples of factual conflict, experiential learning, and working-memory compaction. It is a good final integration read because it exercises formation, evolution, retrieval, traces, metrics, and immutable artifacts together.

The LongMemEval adapters live in `internal/evaluation/longmemeval_v1.go`, `longmemeval_v1_answer.go`, and `longmemeval_v2.go`. Read [`evaluation.md`](evaluation.md) before interpreting their output: adapter smoke tests, retrieval metrics, and model-judged answer quality are different claims.

## Run while reading

Start with narrow tests so each package boundary stays visible:

```sh
go test ./internal/domain ./internal/application
go test ./internal/adapters/sqlite
go test ./internal/api ./sdk
go test ./...
```

Run the deterministic lifecycle benchmark without a model server:

```sh
run_id="reading-guide-$(date +%Y%m%d%H%M%S)"
go run ./cmd/memxplore benchmark internal \
  --run-id "$run_id" --seed 1 --output runs
go run ./cmd/memxplore eval verify \
  --run "runs/$run_id"
```

This produces a manifest, predictions, metrics, replayable traces, and an HTML report. Ollama is not required for this path.

## Find the right change point

| Change | Primary location | Also check |
| --- | --- | --- |
| Domain invariant or memory meaning | `internal/domain` | domain tests, export schema compatibility |
| Formation or evolution behavior | `internal/strategy`, `internal/application` | Strategy Package hash, lifecycle tests |
| Retrieval ranking or filtering | `internal/application/retrieval.go` | SQLite candidate queries, trace fields, evaluation metrics |
| Storage schema | `internal/adapters/sqlite/migrations` | backup, migration, portability, purge tests |
| REST contract | `api/openapi.yaml`, `internal/api` | Go SDK and contract tests |
| MCP tool behavior | `internal/api/mcp.go` | authorization and structured output tests |
| Agent integration | `internal/agentevent` | opt-in ingestion and idempotency tests |
| Benchmark or metric | `internal/evaluation` | immutable manifest fields and report output |

Before changing a research claim, check `research/catalog.json`, [ADR 0003](architecture/adr/0003-research-fidelity-labels.md), and [`strategy-packages.md`](strategy-packages.md). The implementation label and fidelity level are part of the result, not documentation decoration.
