# Feeds

Feeds read outside sources into the index, so recall and `open` can find them. This is M5 step a of [mark 2](mark2-plan.md), section 5: the contract, the journal as a feed, git, and vaults. Activity, mail, calendar and Linear come later.

```sh
vinci feed run [journal|git|vaults ...]    # every enabled feed by default
vinci feed status                          # per feed: cursor, current and total versions, last run
vinci feed rebuild <journal|git|vaults>    # drop the feed's records and cursor, then read it again
# each takes --index PATH (default $VINCI_HOME/index.sqlite)
```

`vinci help` does not list these yet, because its text is compared with the archived C# binary. Feeds write only the index. None of them writes the journal, and nothing runs them on a timer yet (M6).

Code: `mojo/src/feed.mojo` (the feeds, their settings and one run), `feed_store.mojo` (the record contract and the store), `feed_git.mojo`, `feed_vaults.mojo`, `feed_edge.mojo` and `cmd_feed.mojo` (the command). The reads outside Mojo are in `mojo/py/vinci_edge/feed_edge.py`. Tests: `mojo/tests/test_feed_store.mojo`, `test_feed_git.mojo`, `test_feed_vaults.mojo`, `test_feed_run.mojo`.

## The contract

A feed gives records. Each record is:

| Field | Meaning |
| --- | --- |
| `source` | The feed's name: `git` or `vaults`. The journal is `journal`. |
| `id` | Stable within the source: a short commit sha, or `<folder>/<file>.md#<slug>`. |
| `at` | When the record was made: the commit time, or the file's modification time. ISO 8601 with an offset. |
| `kind` | `commit` or `section`. |
| `title` | The subject of a commit, or the breadcrumb of a section (see below). |
| `body_path` | The file a section came from. Empty for a commit, which is generated. Kept as a tag (`body:<path>`) for provenance. |
| `text` | What is stored, searched and opened. Its first line is the title. |
| `hash` | SHA-256 of `text`. A change in the text is a change in the hash. |
| `trusted` | Always false for feeds. Only the journal holds the person's own writing. |
| `links` | Other record ids this one refers to: a commit's parents, a section's enclosing section, and `project:<folder>` for the top-level folders a commit touched. |

A feed has a cursor, the place its last read ended. The index keeps one cursor per feed, in a `feeds` table (name, cursor, last run, summary). A run is a read followed by one transaction. In that transaction the feed writes its new and changed records, retracts the records it no longer sees (only when the read covered the whole source), and moves the cursor. A feed that fails writes nothing, and the other feeds still run.

Records are stored as facts, in the same table as journal facts, keyed by `(source, id)` and versioned by hash:

| Case | Result |
| --- | --- |
| New record | a new fact, counted as new |
| Same hash as the current version | nothing changes |
| New hash | the current version ends and a new one is stored (a version) |
| Record gone from the source | a retraction version: the same text with status `retracted`. Recall skips it. `open` shows it marked retracted |
| A retracted record comes back | a new version, because a retraction's hash never equals a content hash |

## The feeds

| Feed | Reads | Cursor | Records |
| --- | --- | --- | --- |
| `journal` | `$VINCI_HOME/journal.jsonl`, as the index always has | byte offset of the complete lines read, and the SHA-256 of the bytes before that offset | one per line that carries text (see [the index](memory.md#the-index-mark-2-m3-step-2a)) |
| `git` | the first-parent commits of one branch of the monorepo, read-only | `<branch>:<full sha>` | one per commit |
| `vaults` | Markdown files (`.md`, `.markdown`) under the configured folders | per file: relative path, mtime (ns), size, SHA-256 of the text, and its section ids | one per heading section |

**git.** Text is the subject, the commit's message (capped at 2,000 bytes), `commit <id>`, and the changed files with their line counts (at most 40 listed). That is a diffstat, not the diff: full diffs are not stored. A merge shows its change against its first parent, as `--first-parent` does. When the cursor is still an ancestor of the branch tip, only the commits after it are read. When it is not (a rewrite, a reset, a changed branch), every commit is read again, and the records that are no longer there are retracted.

**vaults.** A section runs from its heading to the next heading of any level, so no text is in two records. Headings inside fenced code do not count. The title is the file's first level-1 heading, else the file name. A section's breadcrumb is the title, then the headings above it, then its own heading, so a title reads `Plan > Steps > Detail`. The id slug is lower-case ASCII with dashes. Another section with the same slug in the same file gets `-2`, `-3` ... in document order. The preamble, the text before the first heading, is `top`. The id path is from the root's parent folder, so `Meingrad/...` and `vinci/docs/...` cannot collide.

A changed file is read and re-cut. Its sections that did not change keep their versions. A file whose modification time and size are the same as the cursor is not read at all.

## Citations and `open`

Each record has a citation id, as the journal's facts do. It is a letter and a number:

| Feed | Prefix | Example |
| --- | --- | --- |
| git | `g` | `g220` |
| vaults | `v` | `v1839` |

The journal's own prefixes are `i r n s x p h b` (and `k m d e t`, as the memory import and handles use them); `d` and `L` are read documents and journal lines. `g` and `v` are not used by any of them.

The number is given once, when the record is first stored, and every later version keeps it. A number is never reused: a retracted record keeps its number, and the next new record takes the next one. A feed rebuild starts the numbers again, since it deletes the feed's facts first.

`vinci open` takes either form: `open g220`, or the record id `open <sha10>` for a commit and `open <path>.md#<slug>` for a section. `open` shows the stored text (the snapshot), not the file again. The reasons are below.

Recall returns feed records with `trusted` false. The chat's write confirmation then applies to any write that follows (see [chat](chat.md)).

## Decisions, and why

- **Reuse the facts table.** A feed record is a fact with `source` set to the feed. The table already keys by `(source, record_id)` and versions by hash, which is the contract. A separate table would copy the versioning rules and the FTS wiring. The journal and the feeds share one `valid_until` / `status` vocabulary, so recall and `open` need no new code path.
- **Citation ids are letter-and-number.** The chat's citation check accepts `[letter digits]`. A sha or a path would not be checked, so a commit cites as `g220`, not as its sha. The sha is also the record id, so `open` finds it.
- **The text is the snapshot.** FTS needs the text in the table, so the text is stored anyway. `open` reads it, rather than the file, for three reasons: the citation then shows the text that recall ranked, not a later edit; a deleted section can still be opened and seen as retracted; and an unchanged index never reads the vaults. `body_path` stays as provenance, so the file can be found. The cost is index size: this run added about 7.5 MB for 2,376 records (about 2 MB of text).
- **Retraction is a version, not a delete.** The same rule as journal retraction. The history stays, so `open` can say what was retracted, and a rewrite of the history leaves the old commit readable.
- **The journal's rewrite check.** The journal's cursor was a byte offset, so a journal rewritten to the same length went unnoticed. The cursor now also holds the SHA-256 of the bytes before the offset. A different prefix means the journal's facts are dropped and read again, and the report says so. A journal *shorter* than the offset is still refused (the existing test): a truncated journal is for the person to decide, with `vinci feed rebuild journal`. An index made before this change has no stored prefix; its first update takes the current prefix as the reference, so a rewrite made before that update is not detected.
- **Embedding is not part of a run.** A feed's new records have no vector, and `vinci index embed` embeds them like any fact. A run does not embed, because a run is cheap and an embed is not (see the measurements).

## Configuration

`$VINCI_HOME/feeds.toml` (default `~/.vinci/feeds.toml`) is optional. Without it, the defaults below apply. An unknown key or table is an error, so a typo does not pass silently.

```toml
[git]
enabled = true
path = "~/Steven3zx.com"       # the repository; read-only
branch = "master"

[vaults]
enabled = true
roots = [
  "~/Steven3zx.com/Meingrad",
  "~/Steven3zx.com/Research",
  "~/Steven3zx.com/vinci/docs",
]
max_bytes = 524288             # files over this are skipped (512 KiB)
exclude = []                   # folder names to skip, in addition to the built-in ones
```

- **Never read:** any folder named `cold-storage` (the repository's rule, `janus/cold-storage/README.md`). It cannot be configured, and a root inside one is refused. Also skipped everywhere: hidden folders (`.git`, `.venv` ...), `node_modules`, `__pycache__`, `build`, `dist`, `target`, `bin`, `obj`, `out`, and symbolic links.
- A file whose name contains a tab, a newline or `|` is skipped, since those characters are used as separators in the cursor.
- A root that does not exist stops that feed, and the others still run.

## Measured

Scratch copy of the staging home (342 journal lines), 2026-11-18, with the real monorepo and vaults read in place:

| Feed | Records | First run | Second run | Notes |
| --- | --- | --- | --- | --- |
| journal | 342 lines, 312 facts | 6 ms (rebuild) | 0 new, 0 versions | 1 read body was in `docs/` |
| git | 467 commits (first-parent) | 0.23 s | 0 new, 0 versions, 0 retracted | 373 KB of text |
| vaults | 688 files, 1,909 sections | 0.04 s | 0 new, 0 versions, 0 retracted | 1.69 MB of text, 688 files read |

The index grew from 3.67 MB to 11.17 MB. The second run of all three feeds took 0.08 s.

Embedding the 2,376 feed records has not been done. A single call per record took 2.5 s on average (median 2.2 s) at about 865 characters each, measured on 20 records. At that rate the backlog is about 100 minutes of CPU.

## Limits

- **Change detection is by mtime and size for vault files.** An edit that keeps both the same is not seen until the next change. Delete the cursor line (or `feed rebuild vaults`) to force a read.
- **Only ATX headings (`#` at the start of a line) start a section.** Setext headings (underlined) do not.
- **First-parent history only.** A commit that reached the branch through a merge's second parent is not a record. The merge commit carries its subject.
- **Lexical recall sees feed text immediately. Vectors do not, until embedded.** So feed records compete with journal facts for words, but not for meaning.
- **Nothing runs feeds on a schedule.** The chat's own index update reads only the journal.

## What recall does with them now

This matters before a run is scheduled. With the feeds loaded, the personal set (60 questions, lexical route) changes:

| Route | Hit (48 answerable) | Abstain (12 unanswerable) |
| --- | --- | --- |
| journal only | 35 | 11 |
| journal and feeds | 29 | 1 |

Vault and git text shares ordinary words with the journal (`journal`, `memory`, `vinci`), so every question has feed records in its top 10, and 40 have one at rank 1. Ten abstention questions now get answers from vault or commit text. Ranking or a separate route is needed before feeds join the default recall. That decision is open.
