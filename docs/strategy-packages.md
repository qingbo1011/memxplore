# Strategy Packages

Every executable memory algorithm is represented by a versioned Strategy Package. The canonical SHA-256 package identity binds:

- strategy ID and semantic version;
- implementation path, implementation claim, and research fidelity;
- prompt and JSON schema, when a generator is used;
- normalized parameters and sorted capability/source sets;
- bounded repair policy.

An experiment hash additionally binds the provider, model, and immutable fixture digests. Whitespace, JSON object key order, and set-like input order do not change the hash.

## Research claims

Implementation labels and fidelity are separate:

| Implementation label | Meaning |
| --- | --- |
| `baseline` | minimal comparator, no paper-specific claim |
| `reference` | MemXplore implementation informed by published concepts |
| `adapter` | compatibility layer for an external protocol or dataset |
| `experimental` | exploratory and not yet stable |
| `reproduction` | reported protocol and results have been matched |

Fidelity is one of `none`, `conceptual`, `protocol-compatible`, or `result-verified`. Code rejects a `reproduction` package unless fidelity is `result-verified`. The built-in v0.1 formation strategies are `reference` / `conceptual`; they are not paper reproductions.

## v0.1 formation matrix

Each token-flat functional class has two proposal-only formation packages:

| Function | Generator-free | Assisted |
| --- | --- | --- |
| factual | preserves source content as a provenance-bearing claim | extracts typed predicate, object, and confidence |
| experiential | preserves source content as a lesson with episode/outcome anchors | extracts a concise reusable lesson |
| working | creates task-scoped state, requiring a context/task ID | compacts a goal and current task state |

Both modes emit a typed `Proposal`; neither can mutate storage. Assisted content is decoded with unknown-field rejection and then validated by the domain model. The apply policy remains responsible for authorization, evidence checks, conflict handling, and persistence.
