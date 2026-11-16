# Talking to vinci

`vinci` with no command opens the chat. It is the way to do everything: the chat model (Qwen3-8B, run by Ollama) is in the conversation, and the journal, memory and recall are its tools. `vinci chat` does the same. `vinci pipeline` prints the stages on their own.

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

```sh
ollama pull qwen3:8b && ollama pull qwen3-embedding:4b
ollama create qwen3-8b-vinci -f deploy/models/qwen3-8b-vinci.Modelfile
```

Settings, all optional:

- `CHAT_BASE_URL`: default `http://127.0.0.1:11434`
- `CHAT_MODEL`: default `qwen3-8b-vinci`
- `CHAT_THINK=1`: let the model reason before it answers (slower, and not needed for journal work)
- `MEMORY_API_URL` and `MEMORY_API_KEY`: the memory service

See also `deploy/models/README.md`.
