Status: done

# JavaScript runtime label

## Problem Statement

Janus shows a Snapshot's language as "JavaScript/TypeScript", but a Node project and a Bun project look the same. The Account cannot see which runtime the project targets.

## Solution

Each Snapshot records a runtime alongside its language. The runtime is a separate label: it never changes the stored language name, the language count, or whether a WASM Build is allowed. It is shown in parentheses after the language, for example "JavaScript/TypeScript (Node)".

## Decisions

- **Runtimes:** only Node and Bun are detected. A Snapshot with no runtime shows just its language.
- **Bun** wins whenever a Bun signal is present, even alongside another package manager's lockfile.
- **Bun signals (root only):** a `bun.lock` or `bun.lockb` at the repository root, or a `packageManager` field in the root `package.json` that starts with `bun@`. A `packageManager` declaration is enough without a lockfile.
- **Bun signal not used:** `bunfig.toml` is ignored. The official docs describe it as optional configuration, not as a detection signal.
- **Node:** a root `package.json` with no Bun signal. npm, pnpm, and yarn lockfiles do not change the result.
- **Version pins alone** (`.nvmrc`, `.node-version`, `engines.node`) never produce a runtime without a root `package.json`.
- **Root only:** detection reads the repository root. Nested `package.json` files are ignored, so each Snapshot has one runtime.
- **Storage:** the runtime is stored on the Snapshot at import time, next to the language.
- **Buildability:** runtime is display only. Buildability still depends only on the language.
- **Existing Snapshots:** they keep an empty runtime until the commit is reimported. There is no startup backfill.
- **Where it shows:** the project page shows it in parentheses after the language. The Project and Snapshot JSON include a `runtime` field, which is an empty string when unknown.

## Testing Decisions

- Scanner tests cover each signal, each non-signal, and nested-package rejection.
- A persistence test confirms the runtime is stored with the Snapshot and returned to readers.
