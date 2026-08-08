# 评测

MemXplore 把一次评测视为不可变证据包，而不是一行终端分数。每个 runner 都会创建新目录；如果 run ID 已存在，命令会直接拒绝覆盖。目录包含以下文件：

| 文件 | 用途 |
| --- | --- |
| `manifest.json` | 数据集 revision 与 SHA-256、运行环境、variant、strategy hash、模型身份、限制和已知局限 |
| `predictions.jsonl` | 每个 case 的检索、答案、token、延迟、成本和有界失败证据 |
| `metrics.json` | 聚合质量、ablation、生命周期与系统指标 |
| `traces.jsonl` | 可重放的检索、生命周期或 adapter trace |
| `report.html` | 由同一次 run 生成的独立、转义 HTML 报告 |

制品以只读方式创建，manifest 保存每个文件的 SHA-256。使用结果前应独立复验：

```sh
go run ./cmd/memxplore eval verify --run runs/<run-id>
```

## v0.1.0 证据

所有 release validation run 都使用 Darwin arm64 上的 Go 1.26.4。延迟是本机 wall-clock 数据，不能跨机器直接比较。

| Run | 范围 | 结果 |
| --- | --- | --- |
| `ci-internal-lifecycle` | 3 个确定性 factual、experiential 与 working-memory 场景 | 9 个生命周期不变量全部通过；lexical Recall@5 和 MRR 均为 1.0 |
| `longmemeval-v1-full-20260808-r2` | cleaned v1 全部 500 题，session-level lexical retrieval | Hit@5 0.9702、Recall@5 0.9197、MRR 0.9244、0 个失败 |
| `longmemeval-v2-small-smoke-20260808` | v2 Small 前 10 题，每题 100 条 trajectory | 10/10 schema 与 materialization 检查通过 |
| `longmemeval-v1-local-answer-20260808-r2` | cleaned v1 前 2 题，同一本机模型分别使用和不使用 memory | no-memory 精确准确率 0/2；lexical-memory 为 1/2；0 个失败 |

完整 v1 检索 run 索引了 23,854 个去重 session unit，包含 470 个可回答 case 和 30 个官方 `_abs` case。lexical baseline 在 `_abs` 题上仍会返回 session，因此 retrieval abstention accuracy 为 0；no-memory arm 按定义为 1。这些是检索指标，不是 LongMemEval 官方 model-judged 问答分数。

v2 结果只验证 adapter 完整性。报告中的 Recall@100 和 MRR 1.0 表示官方 Small haystack reference 已按顺序物化，不表示 memory retrieval 或问答质量。这个 smoke test 没有运行 memory backend、截图理解、reader model 或 evaluator。

本地答案 run 使用已安装模型 `hf.co/HauhauCS/Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M`，本机 Ollama digest 为 `f29a8c06f0c41f668c09bba4e9c7f2b3484ca59dcac2bfe8b7d5fa217e47ea1a`。温度为 0，证据预算为 4,096 token。Ollama 原生 adapter 关闭 thinking，让 128-token 输出预算用于最终答案，而不是隐藏推理。准确率使用 `normalized-exact-match-v1`，没有使用 LLM judge。2 个 case 足以验证端到端链路，但不足以估计整体问答质量。

## 数据集固定版本

MemXplore 不分发 benchmark dataset。release validation 使用以下上游 revision：

| 数据集 | Revision | License | 已验证文件 |
| --- | --- | --- | --- |
| `xiaowu0162/longmemeval-cleaned` | `98d7416c24c778c2fee6e6f3006e7a073259d48f` | MIT | `longmemeval_s_cleaned.json`: `d6f21ea9d60a0d56f34a05b609c79c88a451d2ae03597821ea3d5a9678c3a442` |
| `xiaowu0162/longmemeval-v2` | `f152293e235517d504809563c833d7190b8c713b` | Apache-2.0 | `questions.jsonl`: `0a3ae5ebea938c24d7800e1e0b0828e08ae1646f939a53853b2b8cdc08e292b7`；`trajectories.jsonl`: `363cec9a8e87aa8d9101ce4e600aadbf7031d674056ebe4f969e8424abc5f3c6`；Small haystack: `9b5301defb23a088a5f06e45ff8d5f35e569d78305a66d492046a9fff9b46593` |

完整 v2 hash 位于固定上游版本的 `checksums.sha256`。本地 adapter 还会把所选的 3 个文件组合计算成一个 dataset identity，写入 manifest。

## 命令

运行确定性生命周期测试并复验：

```sh
go run ./cmd/memxplore benchmark internal \
  --run-id internal-lifecycle-local \
  --seed 1 \
  --output runs
go run ./cmd/memxplore eval verify \
  --run runs/internal-lifecycle-local
```

取得并校验固定版本的 cleaned v1 数据集后，运行全部 500 个检索 case：

```sh
go run ./cmd/memxplore benchmark longmemeval-v1 \
  --dataset datasets/longmemeval-v1/longmemeval_s_cleaned.json \
  --revision 98d7416c24c778c2fee6e6f3006e7a073259d48f \
  --run-id longmemeval-v1-full-local \
  --top-k 5 \
  --output runs
```

运行有界本地答案对照。命令只接受 loopback Ollama URL，也不会下载模型：

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

把 3 个固定版本文件放在同一 data root 后，运行 v2 Small adapter smoke：

```sh
go run ./cmd/memxplore benchmark longmemeval-v2-small \
  --data-root datasets/longmemeval-v2 \
  --revision f152293e235517d504809563c833d7190b8c713b \
  --limit 10 \
  --run-id longmemeval-v2-small-local \
  --output runs
```

每个 benchmark 还支持显式设置 `--otel-endpoint` 与 `--otel-service-name`。没有 endpoint 时，telemetry 保持关闭。HTTP span 只记录 method、route pattern 与 status，不记录原始 path、query、prompt、memory content、credential 或 identifier。

## 解释规则

- Adapter smoke 的 `passed` 只表示固定 schema 与 reference 被正确处理，不是质量分数。
- `protocol-compatible` 表示 adapter 遵守相关数据契约，不代表论文 reproduction。
- 完整 v1 检索会把 `_abs` case 排除在 Recall@K 和 MRR 之外，另行报告 abstention 行为。
- Provider 失败会保留在 `predictions.jsonl` 并计入 failure rate，不会被静默丢弃。
- 模型生成答案记录 provider token usage，本地 monetary cost 为 0。零成本不等于零计算资源。
- Release asset 只包含 run evidence，不包含上游 dataset 或模型权重。
