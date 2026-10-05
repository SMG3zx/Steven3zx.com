# MeinFactory

Local-first software factory built with Qwik, Mastra, Bun, Ollama, and Qwen.

## Run MeinFactory

```powershell
bun run dev
```

This starts both services from one Bun process tree:

Install Ollama and pull the configured model:

```powershell
ollama pull qwen2.5:14b-instruct
```

The Qwik interface runs at `http://localhost:5173`; the local Mastra agent service runs at `http://127.0.0.1:4111`. The parent process forwards input/output and stops both children together.

## Test the real local model

```powershell
bun run test:ollama
```

This checks the Ollama API, confirms `qwen2.5:14b-instruct` is installed, starts the real Mastra service, submits a factory run, and waits for the approval gate.

The first vertical slice intentionally keeps the agent read-only: workspace tools, isolated worktrees, approvals, and persistence will be added before allowing code changes.
