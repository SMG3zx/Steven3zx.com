# Development

```sh
dotnet test tests
```

The only NuGet package in the program is `Neo4j.Driver`, used by `vinci memory`. The journal commands have no dependencies beyond .NET 10.

| File | Holds |
| --- | --- |
| `src/Journal.cs` | The event record and the append-only file |
| `src/State.cs` | Rebuilding the current state from events |
| `src/App.cs` | The commands and the stage gates |
| `src/Tracker.cs` | Recording window time, commits and use, read from ActivityWatch and git |
| `src/Portal.cs` | The portal: which links go on the page, how it is drawn, and the server |
| `src/Memory/` | `vinci memory`: the Neo4j store (`Neo4jStore.cs`), recall and ranking (`Retrieval.cs`), the API (`Service.cs`), the worker (`Worker.cs`), the MCP server (`Mcp.cs`), the audit log (`Audit.cs`), the web pages (`Pages/`), and the benchmarks (`Evaluation/`) |
| `src/Chat/` | `vinci` and `vinci chat`: the conversation (`Conversation.cs`), the model client (`OpenAiChat.cs`), the tools (`Tools.cs`), the memory API (`MemoryApi.cs`), and the journal-to-memory sync (`JournalSync.cs`) |
| `src/Review.cs` | The findings, the limits, and what to do next |
| `src/Stats.cs` | Control limits |
| `deploy/awatcher.service` | The systemd user unit that runs ActivityWatch |
| `deploy/portal.service` | The systemd user unit that runs `vinci serve` |
| `deploy/memory.service`, `deploy/memory-worker.service` | The systemd user units for `vinci memory serve` and `worker` |
| `deploy/memory/` | The Neo4j compose file and the settings example |
| `deploy/models/` | The Modelfile that builds the chat model, and how to set up both models |
