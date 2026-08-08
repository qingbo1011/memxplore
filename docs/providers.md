# Provider Configuration

The application core depends only on `Generator` and `EmbeddingProvider` ports. The OpenAI-compatible adapter receives an explicit absolute base URL, optional API key, HTTP client, and model on every request. It never reads an endpoint or credential from the environment and never auto-discovers cloud keys.

## Release-validation Ollama profile

MemXplore v0.1.0 release validation uses only the following already-installed local models:

| Capability | Model | Local Ollama digest | Contract |
| --- | --- | --- | --- |
| embeddings | `qwen3-embedding:0.6b` | `ac6da0dfba84a81fdbfbaf330198c33cd77c4cdfc53e8bc50eb581914a15621d` | 1024 dimensions |
| generation | `hf.co/HauhauCS/Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M` | `f29a8c06f0c41f668c09bba4e9c7f2b3484ca59dcac2bfe8b7d5fa217e47ea1a` | schema-constrained formation and synthesis checks |

The configured base URL is `http://127.0.0.1:11434/v1`. Embeddings use that OpenAI-compatible endpoint. Assisted formation and the bounded answer benchmark derive the matching native base URL and call `/api/chat` with thinking explicitly disabled, reserving the output budget for schema-valid final content. The benchmark command rejects non-loopback provider URLs. MemXplore does not pull models automatically. Release and CI code must not call a cloud model; CI uses the deterministic fake provider.

The fake provider records scripted generation requests and produces deterministic, normalized hash embeddings. This makes strategy, retrieval, and evaluation tests reproducible without network access or model-weight drift.
