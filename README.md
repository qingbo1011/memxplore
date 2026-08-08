# MemXplore

[简体中文](README.zh-CN.md)

MemXplore is an executable panorama of agent-memory research: a reference implementation and experiment workbench for studying how agent memory is formed, evolved, retrieved, evaluated, and governed.

The project prioritizes research coverage, learning value, reproducibility, and honest evaluation over product breadth. It is not a production replacement for Mem0 or Zep, and an implementation is never called a paper reproduction unless its protocol and results have been matched.

> Status: v0.1.0 is under active development. The public protocol is already reserved as `/v1`, but no stability claim applies until the `v0.1.0` release is published.

## v0.1 research slice

The first release covers the complete lifecycle of explicit, token-level memory for three functions from *Memory in the Age of AI Agents*:

| Function | Formation | Evolution | Retrieval |
| --- | --- | --- | --- |
| Factual | provenance-bearing claims | conflicts, supersession, valid/system time | cited, trust-aware recall |
| Experiential | episodes, outcomes, lessons | feedback and derived evidence | cases and reusable lessons |
| Working | task-scoped working sets | rule-based TTL and compaction | opt-in, task-local recall |

The baseline retrieval stack is lexical FTS5/BM25, exact cosine semantic search, and reciprocal-rank-fusion hybrid search. When embeddings are configured, hybrid is the default; otherwise the daemon reports an explicit lexical fallback.

Planar/graph and hierarchical memory, latent/parametric labs, complete multimodal memory, HA, enterprise IAM, and an editable web UI are intentionally deferred. See [the roadmap](docs/ROADMAP.md).

## Architecture

One `memxplore` binary provides the daemon, CLI, stdio MCP bridge, HTTP/MCP endpoints, and benchmark runner. A small ports-and-adapters core keeps domain and application code independent of SQLite, HTTP, MCP, Ollama, and cloud SDKs.

```text
CLI / REST / MCP / Go SDK / AgentEvent adapters
                    |
          application contracts
                    |
        domain model and policies
                    |
 SQLite / providers / artifacts / telemetry adapters
```

The single `memxplore serve` daemon owns SQLite, durable jobs, provider calls, and audit state. Large or non-text content is referenced through a content-addressed artifact store rather than embedded in memory records.

## Quickstart

Go 1.26.x is required. Start the generator-free lexical baseline:

```sh
go test ./...
go run ./cmd/memxplore serve --db ./memxplore.sqlite
```

In another shell, capture evidence and recall it:

```sh
go run ./cmd/memxplore remember \
  --owner local --subject local --context demo \
  --function factual --idempotency-key quickstart-1 \
  --text "Ada prefers concise release notes"

go run ./cmd/memxplore recall \
  --owner local --subject local --context demo \
  --mode lexical --query "release notes"
```

Loopback HTTP is tokenless by default. Before binding a non-loopback address, create a scoped token with `memxplore token create`; the daemon stores only its SHA-256 digest. Ollama is opt-in and never auto-pulls models. See [API, CLI, MCP, SDK, and AgentEvent usage](docs/api.md) and [provider configuration](docs/providers.md).

## Evaluation evidence

The v0.1.0 release gates include deterministic lifecycle scenarios, a full 500-case LongMemEval v1 session-retrieval run, a bounded LongMemEval-V2 Small adapter smoke, and a two-case local Ollama answer comparison. The full v1 lexical run reached Recall@5 0.9197 and MRR 0.9244 with zero failures. These are session-retrieval results, not the official model-judged question-answering score.

Every run writes an immutable manifest, predictions, metrics, replayable traces, artifact hashes, and a standalone HTML report. See [evaluation commands, exact dataset pins, results, and limitations](docs/evaluation.md).

## Research integrity

- Every strategy is labeled `baseline`, `reference`, `adapter`, `experimental`, or `reproduction`.
- Model-driven changes first produce typed proposals and pass an observe/propose/apply policy.
- Experiments record immutable manifests, fixtures, seeds, strategy hashes, predictions, metrics, costs, latencies, and failures.
- Cloud-compatible provider configuration may exist, but release validation uses deterministic fakes and the two explicitly documented local Ollama models only.
- The machine-readable [research catalog](research/catalog.json) records source version, taxonomy, implementation status, fidelity, upstream revision/license verification, and benchmarks.

## Security and privacy posture

Ordinary observations and memories are untrusted evidence, never instructions. The design includes scoped principals, namespace isolation, redaction hooks, subject export, distinct archive/forget/purge semantics, non-content deletion receipts, and replayable audit traces. Loopback may be unauthenticated; non-loopback HTTP must use hashed scoped API tokens.

Security issues should be reported according to [SECURITY.md](SECURITY.md).

## Project documents

- [Project charter](docs/CHARTER.md)
- [Architecture decisions](docs/architecture/adr/README.md)
- [Strategy Packages](docs/strategy-packages.md)
- [Provider configuration](docs/providers.md)
- [Retrieval contract](docs/retrieval.md)
- [Memory lifecycle](docs/lifecycle.md)
- [API, CLI, MCP, SDK, and AgentEvent](docs/api.md)
- [Evaluation evidence](docs/evaluation.md)
- [Platform support](docs/platform-support.md)
- [Roadmap](docs/ROADMAP.md)
- [Contributing](CONTRIBUTING.md)
- [Third-party notices](THIRD_PARTY_NOTICES.md)

## License

Apache License 2.0. See [LICENSE](LICENSE).
