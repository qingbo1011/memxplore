# Memory Lifecycle

MemXplore separates immutable evidence, proposal generation, policy, and persistence:

```text
Observation -> typed Proposal -> ApplyPolicy -> Memory / MemoryVersion -> Operation
```

Models and deterministic strategies share the same proposal contract. Neither can write storage directly. A policy decision must carry a reason code; only an allowed proposal reaches the atomic lifecycle repository, which records exactly one idempotent apply operation.

## Factual memory

Factual payloads preserve subject, predicate, typed object, epistemic status, confidence, and observation provenance. Supersession closes the prior system-time interval and, for later effective facts, its valid-time interval. Historical retrieval can therefore recover the version visible at a requested valid/system-time pair. Contradictory overlapping claims can remain current as separate memories under one conflict group; recall preserves both alternatives.

## Experiential memory

An `Episode` records a bounded task trajectory. Each `Outcome` has an independent source and evidence. A lesson must reference real outcomes belonging to its cited episode and namespace. Retrieval usage feedback is stored in a separate table keyed by lesson version, trace, and source, so feedback never silently rewrites the lesson.

## Working memory

A working memory must reference an active `WorkingSet` with the same namespace and task. Task-local context is required for normal recall. Global recall is disabled by default and requires both working-set opt-in and an explicit recall flag. TTL expiry archives the working set and its current task memories.

## Evolution and dependencies

Factual, experiential, and working functions each provide generator-free and schema-constrained assisted evolution packages. Evolution creates immutable new content versions; lifecycle closure fields on the predecessor are finalized atomically. Derived versions carry dependency edges. Updating a parent marks all transitively derived current versions `stale`; a rebuild proposal must name its replacement dependencies and creates a fresh current version. Dependency creation is namespace-bound, duplicate-free, and cycle-checked.

## Archive, forget, and purge

| Operation | Content retained | Search indexes retained | Reversible | Derived handling |
| --- | --- | --- | --- | --- |
| archive | yes | yes, but excluded | policy-dependent | unchanged |
| forget | yes until purge | no | policy-dependent | unchanged |
| purge (default) | no | no | no | recursively purged |
| purge (`mark-stale`) | target only removed | target removed | no | descendants excluded until rebuild |

Every purge finishes with a WAL truncate checkpoint and leaves only an aggregate, non-content receipt.
