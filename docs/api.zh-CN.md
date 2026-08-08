# API、CLI、MCP、SDK 与 AgentEvent

[English](api.md)

## Daemon 与本地模型

安全默认值是免 token 的 loopback daemon，使用 generator-free formation 与 lexical recall：

```sh
memxplore serve --db ./memxplore.sqlite
```

如需启用 v0.1 release-validation Ollama 配置，必须显式指定：

```sh
memxplore serve \
  --db ./memxplore.sqlite \
  --ollama-url http://127.0.0.1:11434/v1 \
  --embedding-model qwen3-embedding:0.6b \
  --embedding-dimensions 1024 \
  --generator-model hf.co/HauhauCS/Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M \
  --enable-assisted
```

命令只使用已经安装的模型，不会下载权重或自动发现 provider key。未设置 `--enable-assisted` 时，`strategy=assisted` 会在入队前被拒绝。

## 鉴权

监听 `127.0.0.1`、`::1` 或 `localhost` 时，本地 principal 可以免 token。监听非 loopback 地址时，如果数据库中没有有效 scoped token，daemon 会拒绝启动；启动后除 health 外的路由都要求 Bearer token。

```sh
memxplore token create \
  --db ./memxplore.sqlite \
  --principal agent-a \
  --owners local \
  --scopes memory:read,memory:write

memxplore serve --db ./memxplore.sqlite --listen 0.0.0.0:7878
```

原始 token 只返回一次，持久化层只保存 SHA-256 摘要。scope 包括 `memory:read`、`memory:write`、`memory:purge` 与 `admin`；`admin` 隐含全部协议能力。owner 白名单以及 shared/public 开关会进一步限制可见记录。

## REST 与 CLI

OpenAPI 3.1 契约位于 [`api/openapi.yaml`](../api/openapi.yaml)。JSON body 会拒绝未知字段以及超过 4 MiB 的请求。主要流程如下：

```sh
memxplore remember \
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

`remember` 会原子写入 Observation 与 durable formation job。等待超时只会返回仍在运行的 job，不会取消它；可用 `job --id JOB_ID` 查询终态。`recall` 返回包含 versioned payload、provenance、score explanation、conflict group 与 retrieval trace 的 `RecallBundle`，不会生成答案。

生命周期命令包括 `archive`、`forget` 与 `purge`。Purge 不可逆，会处理传递派生内容，需要 `memory:purge` scope，CLI 还要求显式传入 `--confirm`。

## MCP

本地 stdio MCP server：

```sh
memxplore mcp --db ./memxplore.sqlite
```

Streamable HTTP 入口为 `POST /v1/mcp`。MemXplore 支持无会话 MCP `2026-07-28` 的 `server/discover`、HTTP routing header、确定性 tool list 与 structured output；同时兼容 `2025-11-25` 和 `2025-06-18` 的 initialize 流程。

已选工具为 `memxplore_job_status`、`memxplore_recall` 与 `memxplore_remember`。每次 tool call 都会重新检查权限，看到工具列表并不代表拥有写权限。

## Go SDK

公开 [`sdk`](../sdk) package 不泄漏 internal application type，提供 `Health`、`Version`、`Remember`、`Recall`、`Job`、`Archive`、`Forget`、`Purge` 与 `IngestAgentEvent`。非 2xx 响应会转换为带 status、code 和 message 的 `*sdk.APIError`。

## AgentEvent v1

只有 daemon 显式设置 `--enable-agent-events` 才会开放 AgentEvent ingestion。`AgentEvent v1` 是 vendor-neutral、opt-in envelope，支持 `message`、`tool_result`、`outcome` 与 `task_state`。事件始终作为 untrusted evidence 入库；receipt、Observation 与 job 原子提交，以 `source + event_id` 重试会返回原 job。

Codex adapter 读取明确提供的 JSONL，不会抓取应用私有状态：

```sh
memxplore ingest codex \
  --file ./codex-events.jsonl \
  --owner local --subject user-a \
  --function factual
```

每行必须包含 `id`、受支持的 `type`、RFC3339 `timestamp` 与非空 `text`；`thread_id`、`turn_id`、`role` 和字符串 metadata 可选。
