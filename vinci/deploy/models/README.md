# Models for vinci

Both models are GGUF files served by llama.cpp's `llama-server`. They are not in git: put them in `models/`.

| Role | Model | Served by | Notes |
| --- | --- | --- | --- |
| Chat (`vinci`, `vinci chat`) and consolidating events into memories | Swift-Qwen3.8-27B IQ3_XXS | `llama-chat.service`, GPU, port 8080 | Tuned by `Swift-Qwen3.8-27B_GGUF.py`; 32k context |
| Embeddings (recall by meaning) | Qwen3-Embedding-4B, 2560 dimensions | `llama-embed.service`, CPU, port 8081 | Same weights Ollama served; the vector index keeps its model name and size |

The chat model fills most of the 12 GB card, so the embedding model runs on the CPU. Units and
settings are in `deploy/llm/`.

Changing the embedding model, or its dimensions, means rebuilding the vector index; the schema
refuses to mix vectors from two models.
