# MemXplore

[English](README.md)

MemXplore 是一套“可执行的 Agent Memory 全景参考实现与实验场”，用于研究 Agent 记忆如何形成、演化、检索、评测和治理。

项目优先保证研究覆盖、学习价值、可复现性和诚实评测，而不是追求业务功能广度。它不是 Mem0 或 Zep 的生产级替代品；除非严格匹配论文协议与结果，否则实现绝不会被称作论文 reproduction。

> 当前状态：v0.1.0 正在开发。公共协议已预留为 `/v1`，但在正式发布 `v0.1.0` 前不作稳定性承诺。

## v0.1 研究切片

首个版本覆盖《Memory in the Age of AI Agents》所定义的三类功能记忆在显式 token-level 形态下的完整生命周期：

| 功能 | Formation | Evolution | Retrieval |
| --- | --- | --- | --- |
| Factual | 带 provenance 的事实主张 | 冲突、替代、有效时间与系统时间 | 带 citation 与 trust 的结构化召回 |
| Experiential | episode、outcome 与 lesson | 反馈和派生证据 | 案例及可复用经验 |
| Working | task-scoped WorkingSet | 规则 TTL 与压缩 | 默认不进入全局召回、任务内按需检索 |

检索基线包含 FTS5/BM25 lexical、精确 cosine semantic，以及使用 RRF 的 hybrid。配置 embedding 后默认使用 hybrid；未配置时 daemon 会明确报告退化为 lexical。

Planar/graph、hierarchical、latent/parametric lab、完整多模态、HA、企业 IAM 和可编辑 Web UI 明确留到后续版本，详见[路线图](docs/ROADMAP.md)。

## 架构

单个 `memxplore` 二进制提供 daemon、CLI、stdio MCP bridge、HTTP/MCP 接口和 benchmark runner。轻量 ports-and-adapters 结构确保 domain/application core 不依赖 SQLite、HTTP、MCP、Ollama 或云厂商 SDK。

```text
CLI / REST / MCP / Go SDK / AgentEvent adapters
                    |
          application contracts
                    |
        domain model and policies
                    |
 SQLite / providers / artifacts / telemetry adapters
```

唯一的 `memxplore serve` daemon 负责 SQLite、durable jobs、provider 调用和审计状态。大型或非文本内容通过 content-addressed artifact store 引用，不直接塞进 memory record。

## 当前开发入口

需要 Go 1.26 或更高版本。

```sh
go test ./...
go run ./cmd/memxplore version --json
```

完整的 serve/remember/recall 使用方式会在相应里程碑完成后写入文档；本 README 不提前宣传尚未实现的命令。

## 研究诚信

- 每个策略都明确标注为 `baseline`、`reference`、`adapter`、`experimental` 或 `reproduction`。
- 模型驱动变更先生成 typed proposal，再通过 observe/propose/apply policy。
- 实验保存不可变 manifest、fixture、seed、strategy hash、prediction、metric、成本、延迟和失败样例。
- 可以保留 OpenAI-compatible provider 配置入口，但 release 验证只使用 deterministic fake 和明确指定的两个本机 Ollama 模型。
- 机器可读的[研究目录](research/catalog.json)记录来源版本、taxonomy、实现状态、fidelity、上游 revision/license 验证与 benchmark。

## 安全与隐私

普通 Observation 和 Memory 一律是“不可信证据”，不是指令。设计包含 scoped principal、namespace 隔离、redaction hook、subject export、archive/forget/purge 区分、非内容删除回执以及可重放审计 trace。Loopback 可以无认证；非 loopback HTTP 必须使用 hash 存储的 scoped API token。

安全问题请按 [SECURITY.md](SECURITY.md) 报告。

## 项目文档

- [项目章程](docs/CHARTER.md)
- [架构决策](docs/architecture/adr/README.md)
- [Strategy Package 设计](docs/strategy-packages.md)
- [Provider 配置](docs/providers.md)
- [路线图](docs/ROADMAP.md)
- [贡献指南](CONTRIBUTING.md)
- [第三方声明](THIRD_PARTY_NOTICES.md)

## License

Apache License 2.0，详见 [LICENSE](LICENSE)。
