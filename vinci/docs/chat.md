# Talking to vinci

`vinci` with no command opens the chat. It is the way to do everything: the chat model (Swift-Qwen3.8-27B, IQ3_XXS, served by llama-server) is in the conversation, and the journal, memory and recall are its tools. `vinci chat` does the same. `vinci pipeline` prints the stages on their own.

```text
you> Make an idea called Flying machine for one pilot.
  · journal {"args":["idea","Flying machine"]}
Created the idea "Flying machine" for one pilot.
```

The model replies with one JSON action when it needs a tool, and the harness runs it:

| Tool | Does |
| --- | --- |
| `journal` | runs any journal command, the same code the command line runs |
| `recall` | searches memory for lines about the question; the model cites them as `[type:id]` |
| `remember` | saves a durable fact or preference |

Each turn is checked. A citation that recall did not return this turn is flagged on the line below the answer. Each exchange is saved to memory, and each new journal fact is sent to memory once, under its journal id, so recall can find a note or a prediction by what it says.

If the memory service is not running, the chat still works on the journal and says once that exchanges are not being saved.

## Setup, once

Two llama-server processes, both started by systemd user units in `deploy/llm/`:

| Unit | Serves | Where | Port |
| --- | --- | --- | --- |
| `llama-chat.service` | Swift-Qwen3.8-27B IQ3_XXS, 32k context, `-ngl 60` | GPU (12 GB) | 8080 |
| `llama-embed.service` | Qwen3-Embedding-4B, 2560 dimensions | CPU only (`-dev none`) | 8081 |

```sh
cp deploy/llm/*.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now llama-chat llama-embed
```

The GGUF files live in `models/`. The chat settings are the ones `Swift-Qwen3.8-27B_GGUF.py` picked
(see `tune_results/`). Re-tune there and update `ExecStart` if the hardware or the model changes.

Settings, all optional:

- `CHAT_BASE_URL`: default `http://127.0.0.1:8080/v1`
- `CHAT_MODEL`: default `swift-qwen3.8-27b` (the `--alias` of the chat unit)
- `CHAT_THINK=1`: let the model reason before it answers (slower, and not needed for journal work)
- `CHAT_HISTORY_CHARS`: how much recent conversation is sent each turn (default 24000 characters)
- `MEMORY_API_URL` and `MEMORY_API_KEY`: the memory service. If nothing answers at `MEMORY_API_URL` (default `http://127.0.0.1:4781`), `vinci` runs the memory API and its worker itself for as long as the chat runs. `MEMORY_EMBEDDED=0` turns that off.
- `VINCI_ENV_FILE`: the settings file `vinci` reads at startup (default `~/.config/vinci/memory.env`). Variables already set in the environment win over it.

See also `deploy/models/README.md`.
