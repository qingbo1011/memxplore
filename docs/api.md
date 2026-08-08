# API, CLI, MCP, SDK, and AgentEvent

[简体中文](api.zh-CN.md)

## Daemon and local providers

The safe default is a tokenless loopback daemon with generator-free formation and lexical recall:

```sh
memxplore serve --db ./memxplore.sqlite
```

To enable the release-validation Ollama profile explicitly:

```sh
memxplore serve \
  --db ./memxplore.sqlite \
  --ollama-url http://127.0.0.1:11434/v1 \
  --embedding-model qwen3-embedding:0.6b \
  --embedding-dimensions 1024 \
  --generator-model hf.co/HauhauCS/Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M \
  --enable-assisted
```

This command uses only already-installed models. It never pulls weights or discovers provider keys. Without `--enable-assisted`, requests selecting `strategy=assisted` are rejected before enqueue.

## Authentication

`127.0.0.1`, `::1`, and `localhost` listeners allow the configured local principal without a token. A non-loopback listener refuses to start until the database has an active scoped token, and then every route except health requires Bearer authentication.

```sh
memxplore token create \
  --db ./memxplore.sqlite \
  --principal agent-a \
  --owners local \
  --scopes memory:read,memory:write

memxplore serve --db ./memxplore.sqlite --listen 0.0.0.0:7878
```

Raw token material is returned once. Only a SHA-256 digest is persisted. Available scopes are `memory:read`, `memory:write`, `memory:purge`, and `admin`; `admin` implies all protocol capabilities. Owner lists and shared/public flags further constrain visible records.

## REST and CLI

The OpenAPI 3.1 source of truth is [`api/openapi.yaml`](../api/openapi.yaml). JSON bodies reject unknown fields and bodies larger than 4 MiB. The main workflow is:

```sh
memxplore remember \
  --url http://127.0.0.1:7878 \
  --owner local --subject user-a --context task-a \
  --function factual --strategy generator-free \
  --idempotency-key observation-42 \
  --text "The user prefers concise release notes" \
  --wait 30000

memxplore recall \
  --owner local --subject user-a --context task-a \
  --query "release note preferences" \
  --mode auto --token-budget 2048 --candidate-limit 20
```

`remember` atomically captures an Observation and durable formation job. A timeout returns the still-running job rather than canceling it. `job --id JOB_ID` reads terminal state. `recall` returns a `RecallBundle` containing versioned payloads, provenance, score explanations, conflict groups, and the retrieval trace; it never returns a generated answer.

Lifecycle commands are `archive`, `forget`, and `purge`. Purge is irreversible, transitively handles derived content, requires `memory:purge`, and the CLI additionally requires `--confirm`.

## Data portability and recovery

Export one subject using an explicit local authorization scope, validate the document, exercise every import constraint without writing, and then import it:

```sh
memxplore data export \
  --db ./source.sqlite \
  --namespace local --principal local-cli --owners local \
  --subject user-a --output ./user-a.memxplore.json

memxplore data validate --input ./user-a.memxplore.json
memxplore data import --db ./target.sqlite \
  --input ./user-a.memxplore.json --dry-run
memxplore data import --db ./target.sqlite \
  --input ./user-a.memxplore.json
memxplore data validate --db ./target.sqlite
```

Exports use `memxplore.subject-export` schema 1, are written with mode `0600`, and refuse to overwrite an existing path. They contain authorized observations, episodes/outcomes referenced by lessons, working sets, stable memories, and every immutable memory version. They exclude API credentials, provider embeddings, durable jobs, retrieval traces, usage telemetry, generated artifact bytes, and model weights. References must be self-contained, subject-bound, acyclic, and visibility-safe or export/import fails.

`data import --dry-run` performs the same transactional inserts, indexes, uniqueness checks, and foreign-key checks before rolling back. A real import into any non-empty database first creates and integrity-checks an online backup beside that database. `data backup` and `data restore` expose the same verified SQLite backup path; restore refuses replacement unless `--overwrite` is explicit, and then preserves the replaced database.

The authenticated REST equivalent is `GET /v1/subjects/{id}/export`. Its authorization comes only from the server-side principal. The Go SDK exposes it as `ExportSubject`.

## MCP

Run a local stdio server with:

```sh
memxplore mcp --db ./memxplore.sqlite
```

Or use Streamable HTTP at `POST /v1/mcp`. MemXplore implements stateless MCP `2026-07-28` with `server/discover`, required HTTP routing headers, deterministic tool listing, and structured tool output. It also accepts the `2025-11-25` and `2025-06-18` initialize flow for existing clients.

Selected tools are:

- `memxplore_job_status`: read a durable job.
- `memxplore_recall`: return structured evidence only.
- `memxplore_remember`: durably capture and form memory.

Tool annotations mark reads and mutations. Authorization is evaluated again at tool invocation, so listing tools never grants write access.

## Go SDK

The public [`sdk`](../sdk) package does not expose internal application types:

```go
client, err := sdk.NewClient("http://127.0.0.1:7878", sdk.WithBearerToken(token))
if err != nil {
    return err
}

remembered, err := client.Remember(ctx, sdk.RememberRequest{
    IdempotencyKey: "example-1",
    Owner: "local", Subject: "user-a", Context: "task-a",
    SourceKind: "example", Function: "factual", Strategy: "generator-free",
    Content: sdk.TextContent("The user prefers concise release notes"),
    WaitMilliseconds: 30000,
})
```

The client also exposes `Health`, `Version`, `Recall`, `ExportSubject`, `Job`, `Archive`, `Forget`, `Purge`, and `IngestAgentEvent`. Non-2xx responses become typed `*sdk.APIError` values.

## AgentEvent v1

Agent ingestion is disabled unless the daemon starts with `--enable-agent-events`. `AgentEvent v1` is a vendor-neutral, opt-in envelope for `message`, `tool_result`, `outcome`, and `task_state`. Events enter storage as untrusted evidence. The receipt, Observation, and job commit atomically; retries keyed by `source + event_id` return the original job.

The Codex adapter consumes explicit JSONL rather than scraping private application state:

```sh
memxplore ingest codex \
  --file ./codex-events.jsonl \
  --owner local --subject user-a \
  --function factual
```

Each line requires `id`, a supported `type`, RFC3339 `timestamp`, and non-empty `text`; `thread_id`, `turn_id`, `role`, and string metadata are optional.
