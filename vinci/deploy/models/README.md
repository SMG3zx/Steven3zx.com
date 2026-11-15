# Models for vinci

vinci runs two models on one local Ollama:

| Role | Model | Set by |
| --- | --- | --- |
| Chat (`vinci`, `vinci chat`) and consolidating events into memories | `qwen3-8b-vinci`, built from `qwen3:8b` | `qwen3-8b-vinci.Modelfile` |
| Embeddings (recall by meaning) | `qwen3-embedding:4b` (Qwen3-Embedding-4B, 2560 dimensions) | `EMBEDDING_MODEL`, `EMBEDDING_DIMENSIONS` |

```sh
ollama pull qwen3:8b
ollama pull qwen3-embedding:4b
ollama create qwen3-8b-vinci -f deploy/models/qwen3-8b-vinci.Modelfile
```

The Modelfile sets a context window of 8192 tokens. Ollama's default is much smaller, and the
OpenAI-compatible route cannot set it per request, so without this the memory context is cut silently.

Both models fit a 12 GB card together at Q4 quantisation, with room for the context. If the card
is short, keep the chat model loaded and let the embedding model load on demand.
