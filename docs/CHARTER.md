# MemXplore Project Charter

## Mission

MemXplore makes agent-memory research executable, inspectable, and comparable. It connects a research taxonomy to concrete lifecycle contracts, reference strategies, deterministic scenarios, and honest benchmark artifacts.

The project is a learning and experimentation substrate. Research clarity wins over feature count, reproducibility wins over demo quality, and an explicit limitation wins over an unsupported claim.

## v0.1 commitments

1. Implement token-level factual, experiential, and working memory across formation, evolution, and retrieval.
2. Preserve evidence and history through provenance, versioning, bitemporal semantics, conflict groups, citations, and traces.
3. Expose one set of application contracts through CLI, REST/OpenAPI, selected MCP tools, and a Go SDK, then enforce their equivalence with contract tests.
4. Provide generator-free and generator-assisted reference strategies for each memory function.
5. Evaluate deterministic no-memory/baseline variants and LongMemEval v1 retrieval; run only a bounded local subset for model-generated answers.
6. Make privacy, permission boundaries, prompt-injection resistance, and verifiable purge release gates.
7. Publish the complete source, evidence, limitations, annotated tag, and GitHub Release as `v0.1.0`.

## Non-goals for v0.1

- Competing with production memory services on scale, availability, or enterprise integrations.
- Planar/graph or hierarchical memory implementations.
- Latent/parametric training as part of the Go runtime; these belong in a later Python lab.
- Full multimodal interpretation, high availability, enterprise IAM, or an editable web UI.
- Treating a compatible implementation as an experimental reproduction without matching upstream protocol and reported results.
- Automatically downloading models, calling discovered cloud credentials, or committing restricted datasets and weights.

## Design principles

- **Explicit lifecycle:** Observation -> Memory/MemoryVersion -> Operation/Retrieval Trace.
- **Portable core:** domain and application packages depend on ports, not transport, storage, model, or vendor adapters.
- **One owner:** a single daemon owns SQLite, durable jobs, providers, and audit state.
- **Typed model boundaries:** model output is a proposal; policy decides whether it may be applied.
- **Evidence before answers:** recall returns a structured bundle with citations, scores, trust, conflicts, and budget information.
- **Safe retention:** archive, decay, forget, and purge are distinct; purge is explicit and irreversible.
- **Honest degradation:** missing embeddings produce a visible lexical fallback, never a silent capability claim.
- **Reproducible research:** fixed fixtures/seeds and immutable artifacts accompany every published result.

## Governance and changes

English code, API, ADR, and specification documents are authoritative. Substantial design choices are recorded as ADRs. Machine-discovered research is a proposal until reviewed and promoted into a versioned research snapshot.

Every verified logical increment should be committed independently using Conventional Commits and pushed without rewriting published `main` history.

