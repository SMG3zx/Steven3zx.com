# Memory for agents

`vinci memory` is a durable memory for AI agents, kept apart from the journal because it needs a database and runs as long-lived processes. Agents save what happened as **events**, and a worker turns events into **memories**, **entities**, **claims** and **procedures** that all point back at the events they came from. Nothing is deleted: a memory is superseded, archived or retracted, and recall hides it. It replaced the separate `personal-memory` project, rewritten in C#.

```sh
docker compose -f deploy/memory/docker-compose.yml up -d    # Neo4j
export NEO4J_URI=neo4j://127.0.0.1:7687 NEO4J_USERNAME=neo4j NEO4J_PASSWORD=change-me

vinci memory serve        # REST API and status pages on http://127.0.0.1:4781
vinci memory worker       # turns queued events into memories
vinci memory mcp          # the tools for an agent, over standard input and output
vinci memory backup       # a snapshot of the whole graph
vinci memory restore <snapshot.json>
```

`vinci memory help` lists the rest, and `deploy/memory/memory.env.example` lists every setting.

**Recall** looks a question up three ways at once (words, meaning through an embedding service if you run one, and the graph around the entities it names), lets each vote by rank, and orders the result for the kind of question it is: *when* questions favour recent and historical memories, *how* questions favour procedures, *who* and *which* questions favour the graph. It answers `abstain` when the evidence does not contain the question's distinctive words, instead of guessing. `build_context` packs the evidence into a text block with a `[type:id]` marker on every line, and `/v1/answers/validate` checks that an answer cites only what was retrieved.

**Connecting an agent.** The MCP server offers 15 tools (`remember_event`, `recall_memory`, `build_context`, `create_memory`, `retract_memory`, and so on). For Claude Code:

```sh
claude mcp add vinci-memory -- vinci memory mcp
```

**Keeping it running:**

```sh
cp deploy/memory.service deploy/memory-worker.service ~/.config/systemd/user/
mkdir -p ~/.config/vinci && cp deploy/memory/memory.env.example ~/.config/vinci/memory.env
systemctl --user enable --now memory memory-worker
```

**Measuring it.** `vinci memory seed` writes a small fixed world and `vinci memory benchmark` asks it 15 questions (`BENCHMARK_STRICT=1` fails on any miss, so it can gate a change). `ablation` repeats that with each way of retrieving on its own, `simulate` plays an invented history that includes a deleted secret and an instruction hidden in an untrusted document, and `longmemeval` runs a LongMemEval file you have downloaded. All of them talk to the running API.

**What changed in the port.** Cypher and routes are as they were. A clash with an earlier event of the same id now answers 409 instead of 500. The status pages are the originals, embedded in the program. Chat is the terminal `vinci` command, which talks to the llama-server model; the memory service no longer runs a web chat.
