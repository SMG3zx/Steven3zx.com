# Repository Guide

## Repository shape

This repository is a monorepo: it contains multiple independent projects and tools under one shared repository and one GitHub remote. A project's dependencies, build steps, and conventions may differ from its neighbors. Before changing a subproject, check its README and any nearer `AGENTS.md` for project-specific guidance.

Some of the main project directories are:

- `janus/` and `janus-web/` — Janus services and web client, with nested Rust and SpacetimeDB projects under `janus/`.
- `rusty-mines/` — Rust Minecraft server and protocol work, including nested crates and tools.
- `eve/`, `qwik/`, `skill-level-up-platform/`, and `mein-factory/` — web and application projects.
- `personal-memory/`, `tcp-typescript/`, `meingrad/`, and `zig-invaders/` — other standalone projects and utilities.

Top-level folders can also contain local infrastructure, archived material, or generated data. Follow `.gitignore` and the relevant project documentation; do not assume every folder is a buildable application.

## Version control: Jujutsu and `origin`

- Use **Jujutsu (`jj`)** for repository operations. This checkout is colocated with Git for interoperability; use `jj` commands instead of Git commands to inspect history, describe changes, manage bookmarks, fetch, and push.
- `origin` is the canonical remote: `https://github.com/SMG3zx/Steven3zx.com.git`.
- Check `jj status`, `jj diff`, and `jj log` before and after making changes. Jujutsu snapshots working-copy edits automatically; there is no separate staging step.
- Once a focused, coherent change exceeds 100 changed lines, create a named Jujutsu commit describing that work, then sync it to `origin`. Do not combine incomplete or unrelated working-copy changes in the commit. Describe the change with `jj describe -m "..."`; use `jj new` when you want to start a separate change on top.
- Fetch from `origin` before integrating remote changes, then push the intended bookmark with `jj git push --remote origin --bookmark <bookmark>`.
- Do not force-update a remote bookmark unless explicitly asked.
- Keep commits focused on the requested work. Do not include local secrets, environment files, build outputs, or unrelated project changes.
