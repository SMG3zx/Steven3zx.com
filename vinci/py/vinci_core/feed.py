"""The feeds (M5 step a, docs/feeds.md): the three feeds that exist, their settings from
$VINCI_HOME/feeds.toml, and what one run does to the index.

  journal  the journal's lines, as the index has always read them (index_build.py). Trusted.
  git      the first-parent commits of a branch (feed_git.py). Not trusted.
  vaults   the Markdown sections of the configured folders (feed_vaults.py). Not trusted.
  activity the window time ActivityWatch recorded, one record per window per day
           (feed_activity.py). Off until [activity] enabled = true. Not trusted.

A run reads, then writes one transaction per feed: the new and changed records, their
retractions when the read saw the whole source, and the feed's cursor. A feed that fails writes
nothing, and the others still run. A second run of the same sources writes nothing.

Port of mojo/src/feed.mojo.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field

from vinci_edge import feed_edge

from .feed_activity import read_activity
from .feed_git import read_git
from .feed_store import (
    FeedRead,
    delete_feed_row,
    delete_source_rows,
    feed_cursor,
    put_record,
    retract_missing,
    set_feed,
)
from .feed_vaults import read_vaults
from .index_build import BuildReport, update_from_journal
from .index_store import IndexStore
from .session import user_error
from .timeutil import parse_instant
from .track import activitywatch_server

JOURNAL_FEED = "journal"
GIT_FEED = "git"
VAULTS_FEED = "vaults"
ACTIVITY_FEED = "activity"
CONFIG_FILE = "feeds.toml"


@dataclass
class FeedConfig:
    """The settings a run needs. Defaults apply when feeds.toml is absent."""

    from_file: bool = False
    git_enabled: bool = True
    git_path: str = ""
    git_branch: str = "master"
    vault_enabled: bool = True
    vault_roots: list[str] = field(default_factory=list)
    vault_max_bytes: int = 524288
    vault_exclude: list[str] = field(default_factory=list)
    activity_enabled: bool = False
    activity_deny: list[str] = field(default_factory=list)
    activity_server: str = "http://127.0.0.1:5600"


@dataclass
class FeedReport:
    """What one feed's run did. read is the records the read saw; new, versions, retracted and
    unchanged are the writes (a retraction is a version too, counted apart)."""

    name: str
    seen: int
    new: int
    versions: int
    retracted: int
    unchanged: int
    seconds: float
    cursor: str
    note: str


def known_feed(name: str) -> bool:
    return name in (JOURNAL_FEED, GIT_FEED, VAULTS_FEED, ACTIVITY_FEED)


def now_seconds() -> float:
    return time.perf_counter()


def load_config(path: str) -> FeedConfig:
    """feeds.toml at path, with the defaults for anything it leaves out. A bad file stops the
    command: a feed that silently reads the wrong folder is worse than no run."""
    status, raw = feed_edge.feed_config(path)
    if status != "ok":
        raise user_error("feeds: " + str(raw))
    git = raw["git"]
    vaults = raw["vaults"]
    activity = raw["activity"]
    cfg = FeedConfig()
    cfg.from_file = bool(raw["from_file"])
    cfg.git_enabled = bool(git["enabled"])
    cfg.git_path = str(git["path"])
    cfg.git_branch = str(git["branch"])
    cfg.vault_enabled = bool(vaults["enabled"])
    cfg.vault_roots = [str(r) for r in vaults["roots"]]
    cfg.vault_max_bytes = int(vaults["max_bytes"])
    cfg.vault_exclude = [str(e) for e in vaults["exclude"]]
    cfg.activity_enabled = bool(activity["enabled"])
    cfg.activity_deny = [str(d) for d in activity["deny"]]
    cfg.activity_server = activitywatch_server()
    return cfg


def summary_of(report: FeedReport) -> str:
    return (
        "seen " + str(report.seen) + ", " + str(report.new) + " new, " + str(report.versions)
        + " versions, " + str(report.retracted) + " retracted, " + str(report.unchanged) + " unchanged"
    )


def apply_read(store: IndexStore, name: str, got: FeedRead, now_iso: str, seconds: float) -> FeedReport:
    """Write one feed's read in one transaction: its records, its retractions when the read saw
    the whole source, and its cursor. The cursor moves only with the records it covers."""
    report = FeedReport(name, got.seen, 0, 0, 0, 0, seconds, got.cursor, got.note)
    store.begin()
    try:
        for rec in got.records:
            code = put_record(store, rec)
            if code == 1:
                report.new += 1
            elif code == 2:
                report.versions += 1
            else:
                report.unchanged += 1
        if got.complete:
            report.retracted = retract_missing(store, name, got.all_ids, now_iso)
        set_feed(store, name, got.cursor, now_iso, summary_of(report))
    except BaseException as err:
        store.rollback()
        raise ValueError(str(err)) from None
    store.commit()
    return report


def run_git(store: IndexStore, cfg: FeedConfig, now_iso: str) -> FeedReport:
    started = now_seconds()
    reading = read_git(cfg.git_path, cfg.git_branch, feed_cursor(store, GIT_FEED))
    return apply_read(store, GIT_FEED, reading, now_iso, now_seconds() - started)


def run_vaults(store: IndexStore, cfg: FeedConfig, now_iso: str) -> FeedReport:
    started = now_seconds()
    reading = read_vaults(cfg.vault_roots, cfg.vault_exclude, cfg.vault_max_bytes, feed_cursor(store, VAULTS_FEED))
    return apply_read(store, VAULTS_FEED, reading, now_iso, now_seconds() - started)


def run_activity(store: IndexStore, cfg: FeedConfig, now_iso: str) -> FeedReport:
    """The window time since the cursor. A server that cannot be read raises, so the run writes
    nothing and the other feeds still run (see run_command)."""
    started = now_seconds()
    reading = read_activity(cfg.activity_server, cfg.activity_deny, feed_cursor(store, ACTIVITY_FEED), parse_instant(now_iso))
    return apply_read(store, ACTIVITY_FEED, reading, now_iso, now_seconds() - started)


def run_journal(store: IndexStore, journal: str, home: str, now_iso: str) -> FeedReport:
    """The journal feed is the index's own journal path (index_build.py). Its cursor is kept in
    meta (the offset and the prefix hash), and mirrored in the feeds table here."""
    started = now_seconds()
    built: BuildReport = update_from_journal(store, journal, home)
    cursor = store.meta_get("journal_offset") + " " + store.meta_get("journal_prefix")
    report = FeedReport(
        JOURNAL_FEED,
        built.lines,
        built.written - built.versions,
        built.versions,
        0,
        built.unchanged,
        now_seconds() - started,
        cursor,
        "",
    )
    if built.rebuilt:
        report.note = "the journal was rewritten, so its facts were read again"
    if built.missing_bodies > 0:
        if len(report.note) > 0:
            report.note += "; "
        report.note += str(built.missing_bodies) + " read bodies missing from docs/"
    store.begin()
    try:
        set_feed(store, JOURNAL_FEED, cursor, now_iso, summary_of(report))
    except BaseException as err:
        store.rollback()
        raise ValueError(str(err)) from None
    store.commit()
    return report


def run_feed(store: IndexStore, name: str, cfg: FeedConfig, journal: str, home: str, now_iso: str) -> FeedReport:
    if name == JOURNAL_FEED:
        return run_journal(store, journal, home, now_iso)
    if name == GIT_FEED:
        return run_git(store, cfg, now_iso)
    if name == ACTIVITY_FEED:
        return run_activity(store, cfg, now_iso)
    return run_vaults(store, cfg, now_iso)


def reset_feed(store: IndexStore, name: str) -> None:
    """Drop a feed's records and cursor, so the next run reads it from the start. For the
    journal, that also clears its offset and prefix hash."""
    store.begin()
    try:
        delete_source_rows(store, name)
        delete_feed_row(store, name)
        if name == JOURNAL_FEED:
            store.meta_set("journal_offset", "0")
            store.meta_set("journal_lines", "0")
            store.meta_set("journal_prefix", "")
    except BaseException as err:
        store.rollback()
        raise ValueError(str(err)) from None
    store.commit()
