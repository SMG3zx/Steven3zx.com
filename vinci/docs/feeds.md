# Feeds

Feeds read outside sources into the index, so recall and `open` can find them. This is M5 steps a and b of [mark 2](mark2-plan.md), section 5: the contract, the journal as a feed, git, vaults, and the activity feed (the window time ActivityWatch recorded). Mail, calendar and Linear come later.

```sh
vinci feed run [journal|git|vaults|activity ...]    # every enabled feed by default
vinci feed status                                   # per feed: cursor, current and total versions, last run
vinci feed rebuild <journal|git|vaults|activity>    # drop the feed's records and cursor, then read it again
# each takes --index PATH (default $VINCI_HOME/index.sqlite)
```

`vinci help` does not list these yet, because its text is compared with the archived C# binary. Feeds write only the index. None of them writes the journal, and nothing runs them on a timer yet (M6).

Code: `mojo/src/feed.mojo` (the feeds, their settings and one run), `feed_store.mojo` (the record contract and the store), `feed_git.mojo`, `feed_vaults.mojo`, `feed_activity.mojo` (the activity feed), `feed_edge.mojo` and `cmd_feed.mojo` (the command). The reads outside Mojo are in `mojo/py/vinci_edge/feed_edge.py` (and the ActivityWatch query in `track_edge.py`, shared with the tracker). Tests: `mojo/tests/test_feed_store.mojo`, `test_feed_git.mojo`, `test_feed_vaults.mojo`, `test_feed_run.mojo`, `test_feed_activity.mojo`.

## The contract

A feed gives records. Each record is:

| Field | Meaning |
| --- | --- |
| `source` | The feed's name: `git`, `vaults` or `activity`. The journal is `journal`. |
| `id` | Stable within the source: a short commit sha, or `<folder>/<file>.md#<slug>`. |
| `at` | When the record was made: the commit time, or the file's modification time. ISO 8601 with an offset. |
| `kind` | `commit`, `section` or `window`. |
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
| `activity` | ActivityWatch's window events, one bucket pair per host (off until enabled) | per window bucket: the start of the last event read | one per window per local day (see [the activity feed](#the-activity-feed)) |

**git.** Text is the subject, the commit's message (capped at 2,000 bytes), `commit <id>`, and the changed files with their line counts (at most 40 listed). That is a diffstat, not the diff: full diffs are not stored. A merge shows its change against its first parent, as `--first-parent` does. When the cursor is still an ancestor of the branch tip, only the commits after it are read. When it is not (a rewrite, a reset, a changed branch), every commit is read again, and the records that are no longer there are retracted.

**vaults.** A section runs from its heading to the next heading of any level, so no text is in two records. Headings inside fenced code do not count. The title is the file's first level-1 heading, else the file name. A section's breadcrumb is the title, then the headings above it, then its own heading, so a title reads `Plan > Steps > Detail`. The id slug is lower-case ASCII with dashes. Another section with the same slug in the same file gets `-2`, `-3` ... in document order. The preamble, the text before the first heading, is `top`. The id path is from the root's parent folder, so `Meingrad/...` and `vinci/docs/...` cannot collide.

A changed file is read and re-cut. Its sections that did not change keep their versions. A file whose modification time and size are the same as the cursor is not read at all.

## The activity feed

`activity` reads the window events ActivityWatch recorded: each stretch a window had the focus, with the time away from the keyboard cut out. The cut is the tracker's own query (`track_edge.py`), so the feed and [the tracker](time-tracking.md) never disagree about what was on screen. The feed is off until `feeds.toml` says `enabled = true`.

**The records: one per window per local day.** A window's record for a day is its app and title, the day's total, and the stretches it had the focus, in local time:

```text
com.microsoft.VSCode: Journal.cs - vinci - Visual Studio Code
2026-11-15: 20m in this window, 09:43-09:50, 10:39-10:40
```

- `title`, which is also the first line of the text: the app, then the window title after `: `. Tabs and line breaks become spaces.
- The second line: the local day, the total (as `span` writes it: days and hours, hours and minutes, or minutes), and up to 20 time ranges. Stretches with at most a minute between them are one range. More than 20 are counted as `(+N more)`.
- `at`: the first stretch of the day, with its local offset. `kind` is `window`, `body_path` is empty, `trusted` is false.
- `id`: `<day>/<16 hex>`. The hex starts the SHA-256 of the app, a unit separator and the title, so the same window on the same day has the same id whichever bucket it came from.

**How a run reads.**

1. It lists the window buckets (`aw-watcher-window_<host>`) and pairs each with its afk bucket (`aw-watcher-afk_<host>`). A window bucket with no afk bucket is skipped, and the report says so: its time cannot be cut to the time at the keyboard, so it is not recorded.
2. For each bucket, it reads the window events from its cursor to now, cut to the not-away periods by the server. The cursor is the start of the last event read, stored as `bucket=start` pairs joined by `;`.
3. For each local day those events touch (the day of each stretch's start and of its end), it reads the whole day again across every bucket and rebuilds that day's records. A record whose total and ranges did not change keeps its hash, so the second run writes nothing.
4. It writes the new and changed records and the cursor in one transaction, as the contract says. It never retracts: the watcher's history does not list every window that ever existed, so a record missing from a read means nothing.

**Denied windows.** A window whose app or title contains one of these terms, without regard to case, is dropped before anything is counted: `1Password`, `Bitwarden`, `KeePass`, `Keychain Access`, `Password Manager`, `Private Browsing`, `Private Window`, `InPrivate`, `Incognito`. The `deny` key in `[activity]` adds terms to that list, and the built-in list always applies, so the config cannot switch the guard off. A denied window is not in the index, and its time is in no total.

**Where it lives.** Its own search table, `fts_activity`, and citation prefix `w`. Not in `fts_feeds`: bm25 counts every row of its table, so activity rows there would move the scores of git and vault answers. `vinci recall --sources activity` searches it. `activity` is in neither the default sources nor `all`.

`vinci feed run` with no names runs activity only when it is enabled, and otherwise prints `activity: off in feeds.toml`. `feed status` shows its cursor as the number of window buckets it holds.

## Citations and `open`

Each record has a citation id, as the journal's facts do. It is a letter and a number:

| Feed | Prefix | Example |
| --- | --- | --- |
| git | `g` | `g220` |
| vaults | `v` | `v1839` |
| activity | `w` | `w4812` |

The journal's own prefixes are `i r n s x p h b` (and `k m d e t`, as the memory import and handles use them); `d` and `L` are read documents and journal lines. `g`, `v` and `w` are not used by any of them.

The number is given once, when the record is first stored, and every later version keeps it. A number is never reused: a retracted record keeps its number, and the next new record takes the next one. A feed rebuild starts the numbers again, since it deletes the feed's facts first.

`vinci open` takes either form: `open g220`, or the record id `open <sha10>` for a commit and `open <path>.md#<slug>` for a section. `open` shows the stored text (the snapshot), not the file again. The reasons are below.

## Recall and the chat

Recall searches the journal only, unless it is asked for more. That is the default of `vinci recall`, of the chat's own recall on the person's words, and of the chat's `recall` tool when it gets no `sources`. So loading feeds does not change any answer about the journal.

```sh
vinci recall --sources git "when did the index change"     # git only
vinci recall --sources git,vaults "..."                    # the two feeds
vinci recall --sources all "..."                           # journal, git and vaults
vinci recall --sources activity "..."                      # the window time (not in all, nor by default)
vinci recall --sources journal "..."                       # the default, named
```

An unknown name is an error (`journal`, `git`, `vaults`, `activity`, or `all`). `all` is the three that were always searched together: `activity` has to be named. The chat's `recall` tool takes the same `sources` argument. The tool instructions tell the model when to use each one: `git` for what changed in the code and when, `vaults` for the notes folders. A line from a feed is labelled with its source in the packed text, `[g220] 2026-11-15 git commit: ...` or `[v1839] ... vault section: ...`, so a commit is not taken for a journal line.

Feed records keep `trusted` false, so the chat's write confirmation applies once one is in the conversation (see [chat](chat.md)).

## Decisions, and why

- **Reuse the facts table.** A feed record is a fact with `source` set to the feed. The table already keys by `(source, record_id)` and versions by hash, which is the contract. A separate table would copy the versioning rules and the FTS wiring. The journal and the feeds share one `valid_until` / `status` vocabulary, so recall and `open` need no new code path.
- **Citation ids are letter-and-number.** The chat's citation check accepts `[letter digits]`. A sha or a path would not be checked, so a commit cites as `g220`, not as its sha. The sha is also the record id, so `open` finds it.
- **The text is the snapshot.** FTS needs the text in the table, so the text is stored anyway. `open` reads it, rather than the file, for three reasons: the citation then shows the text that recall ranked, not a later edit; a deleted section can still be opened and seen as retracted; and an unchanged index never reads the vaults. `body_path` stays as provenance, so the file can be found. The cost is index size: this run added about 7.5 MB for 2,376 records (about 2 MB of text).
- **Retraction is a version, not a delete.** The same rule as journal retraction. The history stays, so `open` can say what was retracted, and a rewrite of the history leaves the old commit readable.
- **The journal's rewrite check.** The journal's cursor was a byte offset, so a journal rewritten to the same length went unnoticed. The cursor now also holds the SHA-256 of the bytes before the offset. A different prefix means the journal's facts are dropped and read again, and the report says so. A journal *shorter* than the offset is still refused (the existing test): a truncated journal is for the person to decide, with `vinci feed rebuild journal`. An index made before this change has no stored prefix; its first update takes the current prefix as the reference, so a rewrite made before that update is not detected.
- **Feeds have their own search table.** Feed records are in `fts_feeds`, and the journal stays in `fts`. FTS5 scores a word by how rare it is across the whole table. With feeds in the journal's table, every journal score moved, and so did the answers: the personal set's default answers changed on about half the questions. In a separate table the journal's scores are the ones they were before feeds. An index that had feed rows in `fts` moves them on its first open.
- **Embedding is not part of a run.** A feed's new records have no vector, and `vinci index embed` embeds them like any fact. A run does not embed, because a run is cheap and an embed is not (see the measurements).

**Activity.** One record per window per day, not per session. A session record would make one record for every switch of focus, so a busy day is hundreds of small records and a question about a day is answered by many of them. A day's record answers "what was I in on Tuesday", and its ranges answer "when". The cost is that a day's record grows and changes while the day runs: a window on screen now has a new version on each run that sees it. Only the records whose totals changed get one, so a run with nothing new writes nothing.

**Activity: the cursor and the day.** The cursor is the start of the last event, so the open event (whose duration still grows) is read again on the next run. Its day is read in full, so no partial total is ever stored as final. The read is incremental by bucket and whole by day, which keeps the cost to the days that changed.

**Activity: the deny list is a floor.** It runs before counting and before any text is stored, and the built-in terms cannot be removed by config. A person adds to it, not around it.

**Activity is off by default.** Window titles are the most personal thing a feed has read so far (page titles, file names, chat windows). Turning it on is a choice in `feeds.toml`.

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

```toml
[activity]
enabled = false                # on: vinci feed run reads the window time from ActivityWatch
deny = ["Journal.cs"]          # added to the built-in list, which always applies
```

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

**Measured (step b).** A scratch copy of the staging home, with the real ActivityWatch read-only, on 2026-11-18:

| | |
| --- | --- |
| first run | 569 records from 3,733 events, 207 ms (one window bucket) |
| second run | 0 new, 0 versions, 0 retracted; 6 ms. The last event read is a zero-length heartbeat, so no day is read again |
| per day | 15 Nov: 362 records (312 min). 16 Nov: 199 (144 min). 17 Nov: 7 (1 min). 18 Nov: 1 (0 min, live) |
| size | 72 KB of text, 127 bytes a record on average. The index went from 3.66 MB to 4.09 MB |
| deny | `journal.cs` removed the record of that window and changed nothing else |

The 17 and 18 November numbers are low for a reason outside the feed: see the limits.

## Measured: what the sources change

Scratch copy with feeds loaded (`real-home`, 2026-11-18), lexical route only, `vinci_eval.index_eval`:

Personal set (60 questions, the journal's own):

| Sources | Hit (48 answerable) | Abstain (12 unanswerable) | Tokens mean |
| --- | --- | --- | --- |
| journal only, no feeds loaded | 35 | 45 of 60 | 200.6 |
| default, feeds loaded | 35 | 45 of 60 | 200.6 (identical per question) |
| `all`, feeds loaded | 25 | 44 of 60 | 5,880 (unpacked top 10) |

The default equals the journal-only run question by question. `all` is worse than the journal on this set: vault and commit text shares ordinary words with the personal questions, and feed scores in the merged list can outrank journal lines.

Feed set (`mojo/eval/feed-questions.json`, 15 answerable questions, eight about commits and seven about notes; each expected string was checked by hand against its commit or file):

| Sources | Hit (15) | Tokens mean |
| --- | --- | --- |
| journal (default) | 1 | 210 |
| `git,vaults` | 13 | 2,724 |
| `all` | 13 | 2,698 |

The two misses under both feed settings are `f-01` (the C# removal commit, whose text is in the index) and `f-15` (the `fn` note in the gotchas, also in the index). Both are ranking misses, not missing text: the answer is in the index but not in the top 10 for that wording. One expected string, `60%`, appears in 123 feed records, so that question is a weak test.

Token means are of the unpacked top 10, as the eval counts them, so they are not what the chat reads (the chat packs to a budget).

## Limits and open decisions

- **Activity: the afk record decides the time.** On this machine the afk watcher wrote no event from 17 Nov 05:26 UTC until 18 Nov 18:18 UTC, and its last `afk` event spans about 36 hours, while the window watcher recorded about 33 hours of focus in that span. The feed drops that time, as the tracker does. Fix the watcher, not the feed ([time-tracking.md](time-tracking.md#when-the-afk-record-is-wrong)).
- **Activity: titles churn.** One day had 362 distinct windows, most of them browser pages and terminal titles. Each is a record, and a record on screen gets a version on each run that sees it.
- **Activity: no vectors.** Like the other feeds, its records are not embedded, so only `--sources activity` lexical recall finds them.
- **Activity: the chat does not ask for it.** The chat's `recall` tool takes `sources` and accepts `activity`, but its instructions do not mention it yet.
- **Activity: the offset is per stretch.** Each stretch's local time uses the offset at its own instant, so a daylight change inside a day shows correct times. The day's bounds use the offset at its midnight.


- Vector recall does not yet see feed records, since they are not embedded. Under `all`, the vector candidates are filtered after the search, so a limit can return fewer records.
- `all` lowers the journal's score on the personal set. Before feeds join the default, the merge across the two tables needs a rule that keeps journal facts first, or a weight. That is open.
