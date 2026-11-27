"""The feed command: `vinci feed run [journal|git|vaults|activity ...]`, `vinci feed status`, and
`vinci feed rebuild <journal|git|vaults|activity>`, with --index PATH on each. All enabled feeds
run when no name is given. Settings are read from $VINCI_HOME/feeds.toml (feed.py). None of the
commands writes the journal.

Port of mojo/src/cmd_feed.mojo.
"""

from __future__ import annotations

from pathlib import Path

from .paths import join_path
from .feed import (
    ACTIVITY_FEED,
    CONFIG_FILE,
    GIT_FEED,
    JOURNAL_FEED,
    VAULTS_FEED,
    FeedConfig,
    FeedReport,
    known_feed,
    load_config,
    reset_feed,
    run_feed,
    summary_of,
)
from .feed_store import count_current, count_versions, feed_cursor, feed_row
from .index_store import IndexStore
from .session import user_error
from .timeutil import Instant, format_instant

INDEX_FILE = "index.sqlite"
USAGE = (
    "usage: vinci feed run [journal|git|vaults|activity ...] | status |"
    " rebuild <journal|git|vaults|activity> [--index PATH]"
)
FEED_NAMES = "(journal, git, vaults, activity)"
SHORT_CURSOR_BYTES = 24


def short(text: str) -> str:
    """The first SHORT_CURSOR_BYTES bytes of a cursor, for a one-line report."""
    raw = text.encode("utf-8")
    if len(raw) <= SHORT_CURSOR_BYTES:
        return text
    return raw[:SHORT_CURSOR_BYTES].decode("utf-8", errors="ignore") + "..."


def cursor_note(name: str, cursor: str) -> str:
    """What a report says about the cursor it left: one line, never the vault listing."""
    if name in (JOURNAL_FEED, GIT_FEED):
        return "cursor " + short(cursor)
    if name == ACTIVITY_FEED:
        buckets = sum(1 for entry in cursor.split(";") if entry != "")
        return str(buckets) + " window bucket" + ("" if buckets == 1 else "s") + " in the cursor"
    files = sum(1 for line in cursor.split("\n") if line != "")
    return str(files) + " files in the cursor"


def report_line(report: FeedReport) -> str:
    millis = int(report.seconds * 1000)
    line = report.name + ": " + summary_of(report) + ", " + str(millis) + " ms"
    if len(report.note) > 0:
        line += " (" + report.note + ")"
    return line + "\n  " + cursor_note(report.name, report.cursor)


def feed_enabled(name: str, cfg: FeedConfig) -> bool:
    if name == GIT_FEED:
        return cfg.git_enabled
    if name == VAULTS_FEED:
        return cfg.vault_enabled
    if name == ACTIVITY_FEED:
        return cfg.activity_enabled
    return True


def run_command(store: IndexStore, names: list[str], cfg: FeedConfig, journal: str, home: str, now_iso: str) -> None:
    explicit = len(names) > 0
    chosen = list(names) if explicit else [JOURNAL_FEED, GIT_FEED, VAULTS_FEED, ACTIVITY_FEED]

    failed: list[str] = []
    for name in chosen:
        if not known_feed(name):
            raise user_error("no feed named '" + name + "' " + FEED_NAMES)
        if not feed_enabled(name, cfg):
            if explicit:
                raise user_error(name + " is turned off in " + CONFIG_FILE)
            print(name + ": off in " + CONFIG_FILE)
            continue
        try:
            report = run_feed(store, name, cfg, journal, home, now_iso)
            print(report_line(report))
        except Exception as err:
            print(name + ": failed, and nothing it read was written: " + str(err))
            failed.append(name)
    if len(failed) > 0:
        raise user_error("feed run failed for " + ", ".join(failed))


def status_command(store: IndexStore, cfg: FeedConfig) -> None:
    for name in [JOURNAL_FEED, GIT_FEED, VAULTS_FEED, ACTIVITY_FEED]:
        if not feed_enabled(name, cfg):
            print(name.ljust(9) + "off in " + CONFIG_FILE)
            continue
        if name == JOURNAL_FEED:
            cursor_text = "offset " + store.meta_get("journal_offset") + " (" + store.meta_get("journal_lines") + " lines)"
        else:
            cursor = feed_cursor(store, name)
            if name == GIT_FEED:
                cursor_text = short(cursor) if cursor != "" else "no cursor"
            else:
                cursor_text = cursor_note(name, cursor)
        last = "never run"
        row = feed_row(store, name)
        if row is not None:
            last = "last run " + row[0] + ": " + row[1]
        print(
            name.ljust(9) + cursor_text + "; " + str(count_current(store, name)) + " current, "
            + str(count_versions(store, name)) + " versions; " + last
        )


def feed_command(args: list[str], home: str, journal: str, now: Instant) -> None:
    index_path = join_path(home, INDEX_FILE)
    positional: list[str] = []
    i = 0
    while i < len(args):
        if args[i] == "--index":
            if i + 1 >= len(args):
                raise user_error("--index needs a path")
            index_path = args[i + 1]
            i += 2
            continue
        positional.append(args[i])
        i += 1
    if len(positional) == 0:
        raise user_error(USAGE)
    sub = positional[0]
    names = positional[1:]

    cfg = load_config(join_path(home, CONFIG_FILE))
    if sub == "status":
        if not Path(index_path).exists():
            raise user_error("no index at " + index_path + "; run vinci feed run")
        status_command(IndexStore(index_path), cfg)
        return
    if sub == "run":
        run_command(IndexStore(index_path), names, cfg, journal, home, format_instant(now))
        return
    if sub == "rebuild":
        if len(names) != 1:
            raise user_error("usage: vinci feed rebuild <journal|git|vaults|activity>")
        name = names[0]
        if not known_feed(name):
            raise user_error("no feed named '" + name + "' " + FEED_NAMES)
        if not feed_enabled(name, cfg):
            raise user_error(name + " is turned off in " + CONFIG_FILE)
        rebuild_store = IndexStore(index_path)
        reset_feed(rebuild_store, name)
        print("rebuilding " + name + " from the start")
        report = run_feed(rebuild_store, name, cfg, journal, home, format_instant(now))
        print(report_line(report))
        return
    raise user_error("unknown feed command '" + sub + "'. " + USAGE)
