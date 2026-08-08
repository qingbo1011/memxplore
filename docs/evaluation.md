# Evaluation

MemXplore treats an evaluation run as an immutable evidence bundle, not a console score. Every runner creates a new directory and refuses to overwrite an existing run ID. The directory contains:

| File | Purpose |
| --- | --- |
| `manifest.json` | Dataset revision and SHA-256, runtime, variants, strategy hashes, model identity, limits, and limitations |
| `predictions.jsonl` | Per-case retrieval, answer, token, latency, cost, and bounded failure evidence |
| `metrics.json` | Aggregate quality, ablation, lifecycle, and system measurements |
| `traces.jsonl` | Replayable retrieval, lifecycle, or adapter traces |
| `report.html` | Standalone escaped report generated from the same run |

Artifacts are created read-only. Their SHA-256 digests are recorded in the manifest. Verify a run independently before using its results:

```sh
go run ./cmd/memxplore eval verify --run runs/<run-id>
```

## v0.1.0 evidence

All release-validation runs used Go 1.26.4 on Darwin arm64. Latencies are local wall-clock measurements and should not be compared across machines.

| Run | Scope | Result |
| --- | --- | --- |
| `ci-internal-lifecycle` | Three deterministic factual, experiential, and working-memory scenarios | All 9 lifecycle invariants passed; lexical Recall@5 and MRR were 1.0 |
| `longmemeval-v1-full-20260808-r2` | All 500 cleaned v1 questions, session-level lexical retrieval | Hit@5 0.9702, Recall@5 0.9197, MRR 0.9244, 0 failures |
| `longmemeval-v2-small-smoke-20260808` | First 10 v2 Small questions, 100 trajectories per question | 10/10 schema and materialization checks passed |
| `longmemeval-v1-local-answer-20260808-r2` | First 2 cleaned v1 questions, same local model with and without memory | No-memory exact accuracy 0/2; lexical-memory exact accuracy 1/2; 0 failures |

The full v1 retrieval run indexed 23,854 unique session units. It evaluated 470 answerable cases and 30 official `_abs` cases. Retrieval abstention accuracy was 0 because the lexical baseline always returned some session for those questions. The no-memory arm had retrieval abstention accuracy 1 by construction. These are retrieval metrics, not the official model-judged LongMemEval question-answering score.

The v2 result is an adapter integrity check. Its reported Recall@100 and MRR of 1.0 only confirm that the official Small haystack references were materialized in order. No memory backend, screenshot understanding, reader model, or evaluator ran in this smoke test.

The local answer run used the installed model `hf.co/HauhauCS/Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M`, local Ollama digest `f29a8c06f0c41f668c09bba4e9c7f2b3484ca59dcac2bfe8b7d5fa217e47ea1a`, temperature 0, and a 4,096-token evidence budget. The native Ollama adapter disabled thinking so the 128-token output budget measured final answers rather than hidden reasoning. Accuracy uses `normalized-exact-match-v1`, not an LLM judge. Two cases are enough to validate the end-to-end path, but not enough to estimate general answer quality.

## Dataset pins

MemXplore does not distribute benchmark datasets. Release validation used these upstream revisions:

| Dataset | Revision | License | Verified files |
| --- | --- | --- | --- |
| `xiaowu0162/longmemeval-cleaned` | `98d7416c24c778c2fee6e6f3006e7a073259d48f` | MIT | `longmemeval_s_cleaned.json`: `d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442` |
| `xiaowu0162/longmemeval-v2` | `f152293e235517d504809563c833d7190b8c713b` | Apache-2.0 | `questions.jsonl`: `0a3ae5ebea938c24d7800e1e0b0828e08ae1646f939a53853b2b8cdc08e292b7`; `trajectories.jsonl`: `363cec9a8e87aa8d9101ce4e600aadbf7031d674056ebe4f969e8424abc5f3c6`; Small haystack: `9b5301defb23a088a5f06e45ff8d5f35e569d78305a66d492046a9fff9b46593` |

The complete v2 hashes are available in the pinned upstream `checksums.sha256`. The local adapter additionally hashes the three selected files into one dataset identity stored in the manifest.

## Commands

Run the deterministic lifecycle suite, then verify it:

```sh
go run ./cmd/memxplore benchmark internal \
  --run-id internal-lifecycle-local \
  --seed 1 \
  --output runs
go run ./cmd/memxplore eval verify \
  --run runs/internal-lifecycle-local
```

After obtaining and hashing the pinned cleaned v1 dataset, run all 500 retrieval cases:

```sh
go run ./cmd/memxplore benchmark longmemeval-v1 \
  --dataset datasets/longmemeval-v1/longmemeval_s_cleaned.json \
  --revision 98d7416c24c778c2fee6e6f3006e7a073259d48f \
  --run-id longmemeval-v1-full-local \
  --top-k 5 \
  --output runs
```

Run a bounded local answer comparison. The command accepts only a loopback Ollama URL and never downloads a model:

```sh
go run ./cmd/memxplore benchmark longmemeval-v1-local-answer \
  --dataset datasets/longmemeval-v1/longmemeval_s_cleaned.json \
  --revision 98d7416c24c778c2fee6e6f3006e7a073259d48f \
  --ollama-url http://127.0.0.1:11434 \
  --model hf.co/HauhauCS/Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M \
  --limit 2 \
  --run-id longmemeval-v1-local-answer-local \
  --output runs
```

Run the v2 Small adapter smoke after placing the three pinned files under one data root:

```sh
go run ./cmd/memxplore benchmark longmemeval-v2-small \
  --data-root datasets/longmemeval-v2 \
  --revision f152293e235517d504809563c833d7190b8c713b \
  --limit 10 \
  --run-id longmemeval-v2-small-local \
  --output runs
```

Every benchmark also accepts an explicit `--otel-endpoint` and `--otel-service-name`. Telemetry is disabled when no endpoint is supplied. HTTP spans record method, route pattern, and status only; raw paths, queries, prompts, memory content, credentials, and identifiers are excluded.

## Interpretation rules

- A `passed` adapter smoke means the pinned schema and references were handled correctly. It is not a quality score.
- A `protocol-compatible` label means the adapter follows the relevant data contract. It is not a paper reproduction.
- Full v1 retrieval excludes `_abs` cases from Recall@K and MRR and reports their abstention behavior separately.
- Provider failures stay in `predictions.jsonl` and count toward failure rate. A run does not silently discard failed cases.
- Model-generated answers report provider token usage and zero local monetary cost. Zero cost does not mean zero compute.
- Release assets contain run evidence, not upstream datasets or model weights.
