# harness

A C# agent harness modeled on [Harness²](https://harness2.github.io/): an agent is a frozen model plus an editable harness, and the harness can be improved by running the agent.

## Design

- **The harness is files.** `HarnessFiles` holds the eight Harness² components: `systemprompt.md` (C1), `guardrails.md` (C2), `memory.md` (C3), `tool-guidance.md` (C4), `subagents/*.md` (C5), `skills/*.md` (C6), `scripts/*` (C7), `plugins/deny.txt` (C8). Edits outside those paths are rejected.
- **The loop is small.** `Agent` calls the model, runs the tools it asks for inside the workspace, and repeats until it stops calling tools or hits the turn limit. Tools: `list_dir`, `read_file`, `write_file`, `load_skill`, `delegate`, `run_script`.
- **The model is behind `IModel`.** `OllamaModel` (the default) talks to the local Ollama server's native `/api/chat` with native tool calls and `think: false`. `AnthropicModel` is an alternative; tests use scripted fakes.
- **Improvement is `Improver`.** Level 1, for one task: run the base harness, ask the model for `k` candidate edits, run each, compose a successor from the evidence, run it. The best-scoring harness wins, and the base wins ties, so a run never ships a regression. Level 2: a reflection is appended to `library/EXPERIENCE.md` and the winner is saved under `library/harnesses/<task>`. Each task starts again from the base harness; only the library carries over.
- **Tasks score themselves.** A task is a prompt, an optional fixture directory and file checks. The score is the fraction of checks that pass.

## Use

```
# Defaults: Ollama at 127.0.0.1:11434, model qwen3:8b, context 16384.
# Override with OLLAMA_HOST, HARNESS_MODEL, HARNESS_NUM_CTX.
# For Claude instead: HARNESS_PROVIDER=anthropic ANTHROPIC_API_KEY=...
dotnet run --project src -- run examples/hello-task.json
dotnet run --project src -- improve examples/hello-task.json --k 3
dotnet test tests
```

`run_script` executes harness scripts as `bash` on this machine and is off unless you pass `--allow-scripts`. The improver can write scripts, so only enable it for tasks and harnesses you trust.

## Not here

The paper's benchmarks, sequential recursion, and per-component domain-invariant versus domain-specific scoping. Candidates see the library's lessons in prose; nothing merges them automatically.
