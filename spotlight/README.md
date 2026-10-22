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

## Security

The API has no authentication and binds to localhost only. Keep `listen_addr`
on `127.0.0.1`. Do not index folders containing secrets you would not want
readable by other local processes.
