# spotlight

Local search over your own files: keyword matching (SQLite FTS5) plus vector
similarity (embeddings from Ollama), combined with Reciprocal Rank Fusion.
A Go daemon indexes configured folders, keeps them current with fsnotify, and
serves search on `127.0.0.1`.

## Status

Scaffold. Working:

- crawl with ignore rules and OneDrive placeholder skipping (Windows)
- text and code extraction (`.md`, `.txt`, source files, configs, …)
- chunking with overlap
- FTS5 keyword search
- Ollama embeddings and brute-force cosine search
- hybrid RRF ranking
- fsnotify watching, debounced, plus periodic rescan
- localhost HTTP API

Not yet: PDF, DOCX and OCR extraction, the floating hotkey UI, and a
sqlite-vec index for very large collections.

## Setup (Windows host)

The daemon runs natively on Windows, not in Docker. fsnotify is unreliable on
Docker Desktop bind mounts, and Ollama needs the GPU directly.

```powershell
winget install GoLang.Go
winget install Ollama.Ollama
ollama pull nomic-embed-text

cd D:\steven3zx.com\spotlight
copy config.example.json config.json   # then edit roots and db_path
go mod tidy                            # first time only: fetches dependencies
```

## Run

```powershell
# one full pass, then exit
go run ./cmd/indexd -config config.json -once

# daemon: initial scan, live watch, API on 127.0.0.1:7788
go run ./cmd/indexd -config config.json

# build a binary
go build -o indexd.exe ./cmd/indexd
```

Query the API from another PowerShell window:

```powershell
Invoke-RestMethod "http://127.0.0.1:7788/search?q=invoice%20template&limit=10"
Invoke-RestMethod "http://127.0.0.1:7788/status"
```

Each result has `path`, `offset` (rune offset of the matched chunk), `snippet`,
`score`, and `sources` (`keyword`, `vector`, or both). Without Ollama running,
search still works using keywords only.

## What gets indexed

Skipped automatically (always on, not configurable away):

- dependency, build and cache folders: `node_modules`, `vendor`, `target`, `dist`, `build`, `.venv`, `.cache`, `.zig-cache`, `zig-pkg`, `.terraform`, `.next`, ... and `.claude/worktrees`
- lock files, `go.sum`, `*.min.js`, `*.map`, SQLite/DB files
- secrets: `.env*`, private keys and certificates (`*.pem`, `*.key`, `id_rsa*`, ...), `.netrc`, `.npmrc`, `kubeconfig`, `app.ini`, `*.tfstate`, and data files named `secrets*`, `credentials*`, `token*`
- the index database itself (`db_path`, `-wal`, `-shm`)
- anything a `.gitignore` under a root excludes (`respect_gitignore`)
- minified or generated files (a line longer than `max_line_bytes` in non-prose files), JSON/YAML/XML/CSV/log files over `max_data_bytes`

Files are capped at `max_chunks_per_file` chunks. Excalidraw drawings are indexed by their text labels.

`ignore_dirs`, `ignore_paths` and `ignore_files` **add** to the built-in lists. `.gitignore` can hide files you still want to search (for example a git-ignored `index.md`); list them in `force_include` using gitignore patterns relative to a root, e.g. `"/index.md"`, `"/tech-func/"`. `force_include` never overrides the built-in rules or secret exclusion.

Identical chunk text is embedded once and its vector reused across files and edits. Existing indexes upgrade in place; changing `embed_model` clears the index so vectors from two models never mix. Each scan prunes entries that are now ignored and compacts the database when it holds a lot of free space.

## Launcher UI

`ui/` holds a hotkey launcher window (default Win+Space) built with Wails. See `ui/README.md`.

## Run at startup (Windows)

```
.\scripts\install-startup.ps1 -Build
```

This builds a windowless `bin\indexd.exe` (and the launcher) and registers two per-user scheduled tasks, no administrator rights needed: **Spotlight Indexer** (30 seconds after logon, logging to `%LOCALAPPDATA%\spotlight\indexd.log`, rotated at 10 MB) and **Spotlight UI** (at logon; the window stays hidden until the hotkey). Both restart if they crash and aren't stopped by the Task Scheduler's 72-hour limit. Re-run the script after rebuilding to update; `scripts\uninstall-startup.ps1` removes them. Start them immediately with `Start-ScheduledTask 'Spotlight Indexer'`. `-SkipUi` installs only the indexer.

The `-log <file>` flag works on any platform and is required for the windowless build, which has no console.

## Console output

The daemon logs to stderr: scan start and finish (with counts and elapsed time), a progress line every 5 seconds during a scan (files indexed/unchanged/skipped, chunks embedded/reused, chunks per second, current file), watcher events, and throttle pauses. Add `-v` to also log every file as it is indexed, skipped or removed.

## Resource limits

Indexing is throttled so it can't pin the machine. These keys in `config.json`
default to 80, and 0 turns a limit off:

```json
"max_cpu_percent": 80,
"max_mem_percent": 80,
"max_gpu_percent": 80,
"embed_batch": 8
```

- **Yielding.** Before every embedding batch, the daemon checks system-wide CPU,
  memory, GPU utilisation and GPU memory. If any is over its limit, indexing
  pauses (re-checking every second) until it drops. So it also backs off when a
  game, build or other GPU job is running.
- **Pacing.** After each batch it idles long enough that the indexer alone keeps
  the GPU busy at most `max_gpu_percent` of the time (80% means 1 part idle for
  every 4 parts busy). Because it also yields to other load, it often settles
  below the limit.
- **Sensors.** CPU and memory come from the OS (Win32 `GetSystemTimes` and
  `GlobalMemoryStatusEx`, or `/proc` on Linux). GPU comes from `nvidia-smi`. At
  startup the daemon logs which sensors work. If `nvidia-smi` is missing, the
  GPU limit falls back to pacing only.
- **Not throttled.** Search queries embed one short string and are not limited.

Utilisation limits bound how *busy* the GPU is, not its peak power draw or
heat. If your machine is unstable under load, also cap board power
(`nvidia-smi -pl 120` as admin) while you diagnose it.

## Layout

```
cmd/indexd/            daemon entry point (flags, wiring, shutdown)
internal/config/       JSON config with defaults
internal/crawl/        directory walk, ignore rules, Windows placeholder detection
internal/extract/      file bytes to text (text formats only for now)
internal/chunk/        overlapping windows on paragraph or word boundaries
internal/embed/        Embedder interface and Ollama client
internal/store/        SQLite schema, FTS5 triggers, BLOB vectors
internal/indexer/      per-file index pipeline and full scan with pruning
internal/throttle/     CPU/memory/GPU limits: pause while busy, pace GPU work
internal/watch/        fsnotify with recursive add and debounce
internal/search/       keyword + vector retrieval, RRF fusion
internal/server/       HTTP handlers
```

## Design notes

- **Pruning guard.** If a root is missing (an unplugged drive), the scan skips
  pruning, so the index is not wiped.
- **Placeholders.** OneDrive online-only files and reparse points (junctions)
  are skipped so that indexing never triggers a download or a loop.
- **Partial embedding.** If Ollama fails mid-file, the chunks without vectors
  are still stored and searchable by keyword. Re-saving the file re-embeds it.
- **Vector search.** Vectors are L2-normalised and cached in memory, reloaded
  after any write. Brute force is fine up to roughly 200k chunks. Beyond that,
  switch to `sqlite-vec` or an ANN index.
- **Single writer.** The store uses one SQLite connection, and the indexer
  serialises writes, so there are no lock contention issues.

## Tuning

- `nomic-embed-text` works best with task prefixes: `search_document: ` on
  indexed text and `search_query: ` on queries. Not applied yet. Add it in
  `indexer.embedRows` and `search.vectorSearch` when you want to compare.
- Changing `embed_model` leaves old vectors in place. Delete `index.db` (or
  the `embedding` column) and run `-once` again.
- `chunk_size` is in characters, not tokens. About 1200 is a reasonable start.
- Point `roots` at folders you actually search (notes, documents, projects).
  Indexing a whole drive pulls in app bundles and minified JavaScript that
  produce thousands of useless chunks.

## Security

The API has no authentication and binds to localhost only. Keep `listen_addr`
on `127.0.0.1`. Do not index folders containing secrets you would not want
readable by other local processes.
