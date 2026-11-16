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
dotnet run --project src -- run examples/tasks/hello.json
dotnet run --project src -- improve examples/tasks/hello.json --k 3
dotnet test tests
```

`run_script` executes harness scripts as `bash` on this machine and is off unless you pass `--allow-scripts`. The improver can write scripts, so only enable it for tasks and harnesses you trust.

## Not here

The paper's benchmarks, sequential recursion, and per-component domain-invariant versus domain-specific scoping. Candidates see the library's lessons in prose; nothing merges them automatically.

## Benchmark: Exercism (Python)

`import-exercism` turns an Exercism track checkout into tasks (stub and tests in the workspace, instructions in `INSTRUCTIONS.md`, reference solution never copied). The score is whether the tests pass. Tests run under bubblewrap with no network, and the agent gets a `run_tests` tool.

```
git clone --depth 1 https://github.com/exercism/python /tmp/exercism-python
dotnet run --project src -- import-exercism /tmp/exercism-python /tmp/ex-py     # 141 tasks
dotnet run --project src -- scan /tmp/ex-py/tasks --limit 40 --samples 3 --turns 25
```

Measured with `qwen3:8b` on 40 random exercises, 3 samples each:

| Harness | Mean pass | Notes |
|---|---|---|
| `examples/default-harness` | 42.5% | 14 tasks always pass, 19 never, 7 sometimes |
| `examples/exercism-harness` (hand-written guardrails) | 45.0% | +2.5 points, SE 4.7: not significant |

The hand-written harness moved individual tasks a lot in both directions (7 better, 4 worse), so the benchmark does react to harness changes. But generic guardrails were a wash overall. Tasks the base harness never passes (about half) look bound by the model, so test harness changes on a random sample, never on those.

The improver with an 8B proposer produced near-identical generic rules from every angle and did not beat the base harness. A stronger proposer model is the next thing to try.
