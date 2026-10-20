# Repository Guide

## Repository shape

This repository is a monorepo: it contains multiple independent projects and tools under one shared repository and one GitHub remote. A project's dependencies, build steps, and conventions may differ from its neighbors. Before changing a subproject, check its README and any nearer `AGENTS.md` for project-specific guidance.

Some of the main project directories are:

- `janus/` — Janus services, including `janus/janus-go` (control plane and web UI), with nested Rust and SpacetimeDB projects under `janus/`.
- `rusty-mines/` — Rust Minecraft server and protocol work, including nested crates and tools.
- `eve/`, `qwik/`, `skill-level-up-platform/`, and `mein-factory/` — web and application projects.
- `personal-memory/`, `tcp-typescript/`, `meingrad/`, and `zig-invaders/` — other standalone projects and utilities.

Top-level folders can also contain local infrastructure, archived material, or generated data. Follow `.gitignore` and the relevant project documentation; do not assume every folder is a buildable application.

Any folder named `cold-storage` holds ideas for much later. Do not read, search, or scan it unless the user explicitly asks you to. See `janus/cold-storage/README.md`.
## Version control: Git and `origin`

- Use **Git** for repository operations: inspect history, stage, commit, fetch, and push with `git`.
- `origin` is the canonical remote: `https://github.com/SMG3zx/Steven3zx.com.git`.
- Check `git status`, `git diff`, and `git log` before and after making changes.
- At the end of agent work, before handing back to the human, commit the work with a descriptive message. Do not leave finished changes uncommitted in the working tree. Do not combine incomplete or unrelated changes in one commit; split them into focused commits. Stage only the paths that belong to each change.
- Fetch from `origin` before integrating remote changes, then push the intended branch with `git push origin <branch>`.
- Do not force-push a remote branch unless explicitly asked.
- Keep commits focused on the requested work. Do not include local secrets, environment files, build outputs, or unrelated project changes.

## Agent skills

### Issue tracker

Linear: issues live in the Engineering team (`ENG`) under the Janus project (`P-ENG-7`). Older local markdown specs under `.scratch/` are history. See `docs/agents/issue-tracker.md`.

### Triage labels

Canonical five-role vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context per project: `janus/janus-go/` has its own `GLOSSARY.md` and `docs/adr/`. See `docs/agents/domain.md`.

### Architecture

`janus/janus-go/` is structured as ports and adapters, with no actor framework in the target. Read `janus/janus-go/docs/architecture.md` (layout, dependency rules, testing standard, migration order) before changing its structure, and see ADR 0003. The migration is tracked under ENG-61.
