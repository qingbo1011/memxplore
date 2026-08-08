# Provider Configuration

The application core depends only on `Generator` and `EmbeddingProvider` ports. The OpenAI-compatible adapter receives an explicit absolute base URL, optional API key, HTTP client, and model on every request. It never reads an endpoint or credential from the environment and never auto-discovers cloud keys.

## Release-validation Ollama profile

MemXplore v0.1 release validation uses only the following already-installed local models:

| Capability | Model | Contract |
| --- | --- | --- |
| embeddings | `qwen3-embedding:0.6b` | 1024 dimensions |
| generation | `hf.co/HauhauCS/Qwen3.5-35B-A3B-Uncensored-HauhauCS-Aggressive:Q4_K_M` | schema-constrained formation and synthesis checks |

The local OpenAI-compatible base URL is `http://127.0.0.1:11434/v1`. The bounded answer benchmark uses the native base URL `http://127.0.0.1:11434` so it can explicitly disable thinking and reserve the output budget for final answers. That command rejects non-loopback provider URLs. MemXplore does not pull models automatically. Release and CI code must not call a cloud model; CI uses the deterministic fake provider.

The fake provider records scripted generation requests and produces deterministic, normalized hash embeddings. This makes strategy, retrieval, and evaluation tests reproducible without network access or model-weight drift.
