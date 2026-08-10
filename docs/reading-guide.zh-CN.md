# 项目阅读导览

[English](reading-guide.md)

这份导览面向想理解实现，而不只是运行二进制文件的读者。MemXplore 按研究问题和明确契约组织代码。阅读这个仓库最快的方法，是先跟踪一条记忆从证据写入到召回的完整链路，而不是依次读完所有 package。

## 选择阅读路线

| 目标 | 建议时间 | 阅读顺序 |
| --- | --- | --- |
| 理解项目边界 | 15 分钟 | [`README.zh-CN.md`](../README.zh-CN.md)、[`CHARTER.md`](CHARTER.md)、[ADR 0001](architecture/adr/0001-v0.1-scope-and-ports-adapters.md)、[`ROADMAP.md`](ROADMAP.md) |
| 跟踪记忆生命周期 | 45 分钟 | `internal/domain`、`internal/application/formation_job.go`、`internal/daemon/formation.go`、`internal/application/lifecycle.go` |
| 理解检索 | 30 分钟 | [`retrieval.md`](retrieval.md)、`internal/application/retrieval.go`、`internal/adapters/sqlite/retrieval.go` |
| 集成 Agent | 30 分钟 | [`api.zh-CN.md`](api.zh-CN.md)、[`api/openapi.yaml`](../api/openapi.yaml)、`internal/api`、`sdk`、`internal/agentevent` |
| 复现评测 | 45 分钟 | [`evaluation.zh-CN.md`](evaluation.zh-CN.md)、`internal/evaluation`、`research/catalog.json` |
| 审计存储与安全 | 45 分钟 | [`storage-safety.md`](storage-safety.md)、`internal/adapters/sqlite`、`internal/auth`、[`SECURITY.md`](../SECURITY.md) |

## 先记住这条主线

主要的写入和读取路径如下：

```text
证据
  -> Observation + 持久化 formation Job
  -> 类型明确的 Proposal
  -> 应用策略
  -> Memory + 不可变 MemoryVersion + Operation
  -> lexical 索引和可选 embedding 索引
  -> RecallBundle + RetrievalTrace
```

这些对象各有明确职责：

| 对象 | 含义 | 从这里开始 |
| --- | --- | --- |
| `Observation` | 从用户、工具或 Agent 事件捕获的不可变源证据 | `internal/domain/observation.go` |
| `Memory` | 稳定身份、功能、作用域和生命周期状态 | `internal/domain/memory.go` |
| `MemoryVersion` | 带来源和双时态元数据的不可变 factual、experiential 或 working payload | `internal/domain/memory.go` |
| `Proposal` | 持久化前必须通过策略判断的类型化候选变更 | `internal/application/proposal.go` |
| `Operation` | 对 observe、propose 或 apply 生命周期动作的审计记录 | `internal/domain/trace.go` |
| `RecallBundle` | 带分数、引用、冲突、预算消耗和 trace 标识的结构化证据 | `internal/application/retrieval.go` |
| `Strategy Package` | 绑定参数、能力、保真度和来源声明的版本化算法身份 | `internal/strategy/package.go` |

这里有两个容易混淆的边界。Observation 是证据，还不是记忆；RecallBundle 是交给 Agent 的证据，不是生成后的答案。

## 按顺序阅读实现

### 1. 确认范围与架构

先读[项目章程](CHARTER.md)和三份[架构决策](architecture/adr/README.md)。它们解释了几个关键选择：v0.1.0 为什么只实现显式 token-level factual、experiential 和 working memory；domain 与 application package 为什么不依赖传输层或 SQLite；大多数策略为什么是参考实现，而不是论文复现。

接着浏览 `cmd/memxplore/main.go`。`runContext` 列出全部命令，`buildRuntime` 是组合入口，负责连接 SQLite、Provider、检索器、formation worker、身份验证、遥测和 HTTP server。

### 2. 掌握领域词汇

持久化代码之前，先读 `internal/domain`。建议顺序如下：

1. `scope.go`：namespace、owner、subject、actor、context 和 visibility。
2. `content.go` 与 `taxonomy.go`：类型化内容和可扩展研究元数据。
3. `observation.go` 与 `memory.go`：源证据、稳定身份和不可变版本。
4. `factual.go`、`experiential.go` 与 `working.go`：三类功能记忆的 payload。
5. `trace.go`：生命周期和检索证据。

各类型的 `Validate` 方法就是可执行文档。`internal/domain/domain_test.go` 中的测试展示了哪些状态和组合会被明确拒绝。

### 3. 跟踪一次 `remember`

按下面的顺序追踪：

1. `internal/api/server.go` 中的 `remember` 和 `prepareRemember` 检查权限与请求限制。
2. SQLite job store 在同一个事务中提交 Observation 和 formation job。
3. `internal/daemon/formation.go` 领取持久化 job，并选择 generator-free 或 assisted formation 策略。
4. `internal/strategy/formation` 生成类型明确的 proposal。
5. `internal/application/lifecycle.go` 校验 proposal，交给 apply policy 判断，再通过 repository 端口记录结果。
6. `internal/adapters/sqlite/lifecycle.go` 原子写入 Memory、不可变 version、provenance 和 operation。
7. 配置 embedding 后，formation worker 还会写入供 semantic retrieval 使用的向量。

job lease 和重试行为位于 `internal/application/jobs.go` 与 `internal/adapters/sqlite/jobs.go`。如果你想研究幂等性或崩溃恢复，应重点阅读这里。

### 4. 跟踪一次 `recall`

先读 [`retrieval.md`](retrieval.md) 中的契约，再看 `internal/application/retrieval.go` 里的 `Retriever.Recall`。

repository 会在排序前应用作用域、可见性、生命周期、记忆功能、有效时间和系统时间过滤。lexical candidate 来自 SQLite FTS5/BM25。semantic candidate 使用配置的 `EmbeddingProvider` 和精确 cosine 评分。hybrid 模式通过 reciprocal rank fusion 合并两种排名，然后去重、归组冲突、应用 token 预算、记录分数解释并写入 retrieval trace。

SQL 和 FTS query 处理位于 `internal/adapters/sqlite/retrieval.go`。先读 `internal/application/retrieval_test.go`，再读 `internal/adapters/sqlite/retrieval_test.go`，会比直接进入实现更容易理解预期行为。

### 5. 对照各个接口

所有公共接口都映射到相同的 application contract：

- `cmd/memxplore/main.go` 实现 CLI 和本地进程组合。
- `internal/api/server.go` 实现 REST、身份验证、请求边界和生命周期路由。
- `internal/api/mcp.go` 实现 stdio 与 Streamable HTTP MCP，只开放精选工具。
- `api/openapi.yaml` 是 REST 的权威定义。
- `sdk` 是公开 Go 客户端，不得暴露内部 application type。
- `internal/agentevent` 校验厂商中立的 AgentEvent v1 envelope，以及显式的 Codex JSONL adapter。

可以把 `internal/api/server_test.go`、`internal/api/mcp_test.go`、`sdk/client_test.go` 和 `cmd/memxplore/main_test.go` 当作契约示例阅读。

### 6. 理解持久化与生命周期安全

`internal/adapters/sqlite/migrations` 按顺序展示存储 schema。`store.go` 配置 SQLite，`backup.go` 保护 migration 和 restore，`portable.go` 实现 subject export/import，`purge.go` 处理不可逆的传递删除。

阅读 [`lifecycle.md`](lifecycle.md) 时，对照 `internal/application/lifecycle.go` 和 `internal/adapters/sqlite/lifecycle.go`。archive、forget 和 purge 是不同操作。purge 绝不会自动触发，执行后只能留下不含原始内容的回执，不能保留可恢复的记忆内容。

### 7. 最后阅读评测

`internal/evaluation/internal.go` 包含 factual 冲突、experiential 学习和 working-memory 压缩的最小完整示例。它把 formation、evolution、retrieval、trace、metric 和不可变 artifact 串在一起，适合作为最后一段集成代码阅读。

LongMemEval adapter 位于 `internal/evaluation/longmemeval_v1.go`、`longmemeval_v1_answer.go` 和 `longmemeval_v2.go`。解释输出前先读 [`evaluation.zh-CN.md`](evaluation.zh-CN.md)：adapter smoke test、检索指标和由模型判断的答案质量是三种不同的声明。

## 边读边运行

先运行范围较小的测试，让 package 边界保持清晰：

```sh
go test ./internal/domain ./internal/application
go test ./internal/adapters/sqlite
go test ./internal/api ./sdk
go test ./...
```

不启动模型服务器也能运行确定性生命周期 benchmark：

```sh
run_id="reading-guide-$(date +%Y%m%d%H%M%S)"
go run ./cmd/memxplore benchmark internal \
  --run-id "$run_id" --seed 1 --output runs
go run ./cmd/memxplore eval verify \
  --run "runs/$run_id"
```

这会生成 manifest、prediction、metric、可重放 trace 和 HTML 报告。该路径不需要 Ollama。

## 找到正确的修改入口

| 修改内容 | 主要位置 | 同时检查 |
| --- | --- | --- |
| 领域不变量或记忆语义 | `internal/domain` | domain test、export schema 兼容性 |
| formation 或 evolution 行为 | `internal/strategy`、`internal/application` | Strategy Package hash、lifecycle test |
| 检索排序或过滤 | `internal/application/retrieval.go` | SQLite candidate query、trace 字段、评测指标 |
| 存储 schema | `internal/adapters/sqlite/migrations` | backup、migration、portability、purge test |
| REST 契约 | `api/openapi.yaml`、`internal/api` | Go SDK 和契约测试 |
| MCP 工具行为 | `internal/api/mcp.go` | 授权与结构化输出测试 |
| Agent 集成 | `internal/agentevent` | opt-in ingestion 和幂等性测试 |
| benchmark 或 metric | `internal/evaluation` | 不可变 manifest 字段和报告输出 |

修改研究声明前，请检查 `research/catalog.json`、[ADR 0003](architecture/adr/0003-research-fidelity-labels.md)和 [`strategy-packages.md`](strategy-packages.md)。implementation label 和 fidelity level 是结果的一部分，不是装饰文档的标签。
