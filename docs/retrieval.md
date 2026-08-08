# Retrieval Contract

MemXplore retrieval returns a `RecallBundle`: typed memory payloads, provenance, conflict groups, score explanations, and an immutable decision trace. It never presents the bundle as an answer. Query rewriting, reranking, and answer synthesis are separate optional Strategy Packages and cannot silently alter baseline recall.

## Modes

- `lexical` uses SQLite FTS5/BM25. User text is tokenized into quoted terms instead of being interpreted as raw FTS syntax.
- `semantic` embeds the query and computes exact float32 cosine similarity over every eligible vector. A request fails when the authorized candidate set exceeds the explicit 10,000-vector exact-scan bound; it never silently changes to approximate search.
- `hybrid` combines independent lexical and semantic ranks with reciprocal rank fusion (RRF, `k=60`).
- `auto` selects hybrid when an embedding provider is configured. Otherwise it records `embedding_not_configured` and uses lexical. Provider failure or absence of compatible stored embeddings also produces an explicit lexical fallback reason.

## Filtering order

SQLite applies the following filters before returning content or vectors:

1. exact namespace and subject;
2. authorized private owners and explicit shared/public grants;
3. active memory plus the immutable version visible at the requested system time (current or superseded history);
4. valid-time and system-time half-open intervals;
5. requested functional classes;
6. task context for working memory. With no matching context, working memory is excluded.

Application ranking preserves conflicting alternatives under a shared conflict-group ID. Exact normalized duplicates remain in the trace with `duplicate_of` but are not selected twice. Candidate selection cannot exceed the caller's token budget, and trace validation recomputes the selected-token sum.

## Score explanation

Each candidate keeps independent raw BM25, cosine, RRF, trust, and final ranking values as applicable. Trust is a deterministic signal derived from the typed payload's epistemic status; it is not conflated with semantic similarity or provider confidence.
