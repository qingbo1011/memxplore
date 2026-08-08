# ADR 0003: Research fidelity labels

- Status: Accepted
- Date: 2026-08-08

## Context

Agent-memory systems frequently combine ideas from multiple papers while reporting results on modified fixtures, prompts, models, or metrics. Calling such work a reproduction is misleading.

## Decision

Every executable research strategy and catalog entry carries exactly one implementation label:

- `baseline`: a minimal comparator with no claim of representing a particular paper.
- `reference`: a usable implementation informed by published concepts but designed for MemXplore contracts.
- `adapter`: compatibility code for an external dataset, protocol, or framework.
- `experimental`: an exploratory design without a stable fidelity claim.
- `reproduction`: an implementation that matches the cited protocol, inputs, model/configuration, metrics, and comparison procedure closely enough to test the reported result.

Fidelity is recorded separately as `none`, `conceptual`, `protocol-compatible`, or `result-verified`. A reproduction requires `result-verified`, upstream revision/license records, and an immutable experiment artifact that explains all deviations.

## Consequences

Most v0.1 memory strategies are labeled reference implementations. Benchmark ingestion can be protocol-compatible without claiming reproduction. Documentation and reports must surface limitations instead of collapsing these distinctions.

