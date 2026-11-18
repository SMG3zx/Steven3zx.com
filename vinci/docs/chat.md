# Talking to vinci

`vinci` with no command opens the chat. It is the way to do everything: the chat model (Swift-Qwen3.8-27B, IQ3_XXS, served by llama-server) is in the conversation, and the journal, the index (recall) and `open` are its tools. `vinci chat` does the same. `vinci pipeline` prints the stages on their own.

```text
you> Make an idea called Flying machine for one pilot.
  · journal {"args":["idea","Flying machine"]}
Created the idea "Flying machine" for one pilot.
```

The model replies with one JSON action when it needs a tool, and the harness runs it:

| Tool | Does |
| --- | --- |
| `journal` | runs any journal command, the same code the command line runs |
| `recall` | searches the index for lines about the question; the model cites them as `[id]` |
| `remember` | saves a durable fact or preference as a journal line |
| `read` | saves a text file (`.txt`, `.md`) or a web page the person named, as a document with its passages, so recall can find them |
| `open` | reads one fact or passage by its id, with `lines=` or `grep=` |

Sources are attributed. A passage from a document is cited as `[d1/2]` and carries the title and the path or address it was read from, with the date it was read. vinci lists those sources under the answer itself; the model never writes that list. A cited passage with no source is flagged, and the answer is not saved.

Each turn is checked. A citation that recall did not return this turn is flagged on the line below the answer. Each exchange is written to the journal as an `exchange` line: the person's message always, and the answer only when it is kept. An answer is kept only when it cites something recalled and every cited passage has a source; otherwise the chat says `answer not saved`. Each write is followed by an index update, so the next recall sees it.

Nothing is sent to a memory service. Neo4j and the memory API were removed in mark 2, and the journal and the index replace them.

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
- `EMBEDDING_BASE_URL` and `EMBEDDING_MODEL`: the embedding server for meaning search (default `http://127.0.0.1:8081/v1`, `qwen3-embedding:4b`). Without it, recall is words only, and says so.
- `VINCI_ENV_FILE`: the settings file `vinci` reads at startup (default `~/.config/vinci/memory.env`; the name is kept from the memory service). Variables already set in the environment win over it.

See also `deploy/models/README.md`.
