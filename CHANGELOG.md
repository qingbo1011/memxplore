# Changelog

All notable changes to MemXplore are documented in this file. The project follows Semantic Versioning for the program while versioning the public protocol, SQLite schema, and portable export schema independently.

## [0.1.0] - 2026-08-08

### Added

- Token-level factual, experiential, and working-memory formation, evolution, retrieval, and governed lifecycle operations.
- Provenance-bearing factual claims with epistemic status, conflict groups, supersession, and valid/system time.
- Outcome-aware experiential episodes and task-scoped, recoverable working sets with goal-preserving compaction.
- FTS5/BM25 lexical, exact-cosine semantic, and RRF hybrid retrieval with citations, trust, conflict, budget, and score explanations.
- Generator-free baselines and generator-assisted reference strategies behind provider-neutral ports and versioned Strategy Packages.
- One CGO-free binary containing the daemon, CLI, REST/OpenAPI server, selected MCP tools, stdio MCP bridge, Go SDK, AgentEvent v1 adapters, and benchmark runner.
- Durable idempotent jobs, crash recovery, OpenTelemetry, scoped hashed API tokens, portable subject export/import, dry-run validation, and verified SQLite backup/restore.
- Immutable evaluation bundles and static reports for deterministic lifecycle scenarios, LongMemEval v1, and LongMemEval-V2 Small adapter checks.
- Reproducible Darwin arm64, Linux amd64, Linux arm64, and Windows amd64 release archives built with Go 1.26.

### Security

- Treats ordinary observations and memories as untrusted evidence, not instructions.
- Enforces namespace, owner, subject, context, visibility, and dependency boundaries during retrieval and lifecycle application.
- Requires scoped authentication for non-loopback HTTP and stores only token digests.
- Separates archive, forget, and irreversible purge; purge tests cover content, indexes, and SQLite WAL residue.
- Adds explicit release gates for persistent prompt injection, permission bypass, cross-scope/time disclosure, private-to-shared/public derivation, and purge residue.

### Evaluation

- The full 500-case cleaned LongMemEval v1 lexical session-retrieval run reports Hit@5 0.9702, Recall@5 0.9197, MRR 0.9244, and zero failures across 470 answerable and 30 official abstention cases.
- The bounded two-case local Ollama answer comparison reports exact accuracy 0/2 without memory and 1/2 with lexical memory. This validates the path but is not a quality estimate.
- The ten-case LongMemEval-V2 Small result validates schema and materialization only; it is not a full benchmark score.
- The local Codex + Ollama E2E verifies generator-free ingest, assisted formation, hybrid recall, portable round-trip, backup/restore, and all explicit security gates.

### Known limitations

- This is a research reference and experiment workbench, not a production replacement for Mem0 or Zep.
- Graph, hierarchical, latent/parametric, and complete multimodal memory are deferred.
- High availability, enterprise IAM, managed deployment, and an editable web interface are outside v0.1.0.
- Tier 2 platforms are cross-build validated but do not have release E2E runtime evidence.
- No implementation is labeled a paper reproduction unless protocol and result evidence are both matched.

[0.1.0]: https://github.com/qingbo1011/memxplore/releases/tag/v0.1.0
