# Monorepo Tool Extraction Map

Discovery pass: 2026-10-04  
Assumption: “another tool” means `meingrad`, the repository-level catalog and task runner. This is an extraction plan; no application code has been moved.

## Candidate map

| Source project | What could be hosted or reused | Recommendation |
|---|---|---|
| `janus/` | `Janus.js` already exposes build, test, local stack, logs, operator, and Go debt-analysis commands. | Register its useful command entrypoints in `meingrad`; keep orchestration and product behavior in Janus. |
| `janus-web/` | OpenTelemetry dependencies and a starter Express server. The current files are a Bun scaffold, not a reusable telemetry package. | Do not extract yet; first define and implement a real shared telemetry API. |
| `mein-factory/` | Qwik/Mastra local agent UI and an Ollama integration smoke command. | Expose project-native checks through `meingrad`; keep agent and approval behavior in MeinFactory. |
| `personal-memory/` | MCP server, backup/restore, smoke commands, and a Codex hook that sends conversations to the memory API with a local retry spool. | Keep the service separate. The hook could become an opt-in connector later; preserve its explicit destination and privacy controls. |
| `rusty-mines/` | `tools/jev` contains a typed-review CLI and evidence/progress report pipeline; `tools/Extract*` and catalog generators support protocol research. | Best reusable-tool candidate: expose Jev's local/offline review commands through `meingrad`, but keep its implementation separate until its Rusty Mines paths and review assumptions are configurable. Keep protocol extractors version-specific. |
| `eve/` | `Hermes/Hermes.js` is a typed ECS runtime with scheduling and diagnostics, used by EVE's runtime adapter. | Best reusable-library candidate: package Hermes behind a documented API and test it independently before sharing it with other apps. |
| `tcp-typescript/` | TCP packet metadata parsing/capture bridge and a SpacetimeDB ingestion module. The capture path relies on Windows `pktmon` and elevated access. | Register safe local commands in `meingrad`; consider extracting the parser/event contract only if another consumer appears. Keep OS capture and database adapters explicit. |
| `skill-level-up-platform/` | A Qwik application and static build setup. | Keep as an app; expose its existing build command through the catalog. |
| `qwik/` | A generic Qwik starter scaffold. | Could become a project template, but compare it with `skill-level-up-platform` first; they currently use different dependency versions and serve different purposes. |
| `zig-invaders/` | Standalone Zig game and Zig build configuration. | Keep as an app; use it as a native-toolchain task entry in `meingrad`. |
| `janus/janus-spacetimedb/` nested tools | `janus/janus-spacetimedb/tools/generate_bindings.py` wraps SpacetimeDB binding generation for that module. | Add a named project task if this workflow needs a repository-wide entrypoint; do not move module-specific generated bindings into `meingrad`. |
| `meingrad/` | Project catalog, native command runner, Jujutsu-aware change/affected selection, safety scan, and catalog doctor. | This is the right host for cross-project metadata and invocation. Add adapters/configuration, not the projects' application code. |

## First extraction boundary

Start by making `meingrad` a reliable index and launcher: list each maintained project and any independently runnable nested tool, then expose its existing build, test, smoke, or review commands as argument arrays. The project keeps owning its implementation, dependencies, credentials, and local state.

The clearest later code extraction is Hermes as a small shared library. The clearest later standalone utility is Jev's evidence-review harness, after separating generic request/response validation from Rusty Mines progress and acceptance policy. Neither should be copied into `meingrad.py`.

## Keep outside the shared tool

The root `.gitignore` identifies Caddy/Gitea state, a duplicate `MeinFactory` checkout, staging material under `projects/`, operational `tech-func/` content, and a private generated Janus documentation vault. They were not treated as extraction sources. Do not centralize their credentials, databases, runtime state, private operational data, or generated outputs.

## Next concrete step

Expand `meingrad/projects.json` with verified task entrypoints for maintained projects and nested tools, including a dependency direction that reflects actual consumers. Run `meingrad doctor` after catalog changes. Keep each project's own package manager and lockfile; this repo is a monorepo, but it is not one unified package workspace.
