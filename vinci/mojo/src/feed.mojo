# The feeds (M5 step a, docs/feeds.md): the three feeds that exist, their settings from
# $VINCI_HOME/feeds.toml, and what one run does to the index.
#
#   journal  the journal's lines, as the index has always read them (index_build.mojo). Trusted.
#   git      the first-parent commits of a branch (feed_git.mojo). Not trusted.
#   vaults   the Markdown sections of the configured folders (feed_vaults.mojo). Not trusted.
#
# A run reads, then writes one transaction per feed: the new and changed records, their
# retractions when the read saw the whole source, and the feed's cursor. A feed that fails
# writes nothing, and the others still run. A second run of the same sources writes nothing.

from std.python import Python

from feed_edge import feed_config
from feed_git import read_git
from feed_store import (
    FeedRead,
    delete_feed_row,
    delete_source_rows,
    feed_cursor,
    put_record,
    retract_missing,
    set_feed,
)
from feed_vaults import read_vaults
from index_build import BuildReport, update_from_journal
from index_store import IndexStore
from session import user_error

comptime JOURNAL_FEED = "journal"
comptime GIT_FEED = "git"
comptime VAULTS_FEED = "vaults"
comptime CONFIG_FILE = "feeds.toml"


struct FeedConfig(Movable):
    """The settings a run needs. Defaults apply when feeds.toml is absent."""

    var from_file: Bool
    var git_enabled: Bool
    var git_path: String
    var git_branch: String
    var vault_enabled: Bool
    var vault_roots: List[String]
    var vault_max_bytes: Int
    var vault_exclude: List[String]

    def __init__(out self):
        self.from_file = False
        self.git_enabled = True
        self.git_path = String()
        self.git_branch = String("master")
        self.vault_enabled = True
        self.vault_roots = List[String]()
        self.vault_max_bytes = 524288
        self.vault_exclude = List[String]()


@fieldwise_init
struct FeedReport(Movable):
    """What one feed's run did. read is the records the read saw; new, versions, retracted
    and unchanged are the writes (a retraction is a version too, counted apart).
    """

    var name: String
    var seen: Int
    var new: Int
    var versions: Int
    var retracted: Int
    var unchanged: Int
    var seconds: Float64
    var cursor: String
    var note: String


def known_feed(name: String) -> Bool:
    return name == JOURNAL_FEED or name == GIT_FEED or name == VAULTS_FEED


def now_seconds() raises -> Float64:
    var time = Python.import_module("time")
    return Float64(String(time.perf_counter()))


def load_config(path: String) raises -> FeedConfig:
    """feeds.toml at path, with the defaults for anything it leaves out. A bad file stops
    the command: a feed that silently reads the wrong folder is worse than no run.
    """
    var reply = feed_config(path)
    if reply.status != "ok":
        raise user_error("feeds: " + String(reply.value))
    var raw = reply.value.copy()
    var git = raw["git"]
    var vaults = raw["vaults"]
    var cfg = FeedConfig()
    cfg.from_file = String(raw["from_file"]) == "True"
    cfg.git_enabled = String(git["enabled"]) == "True"
    cfg.git_path = String(git["path"])
    cfg.git_branch = String(git["branch"])
    cfg.vault_enabled = String(vaults["enabled"]) == "True"
    for r in vaults["roots"]:
        cfg.vault_roots.append(String(r))
    cfg.vault_max_bytes = Int(String(vaults["max_bytes"]))
    for e in vaults["exclude"]:
        cfg.vault_exclude.append(String(e))
    return cfg^


def summary_of(report: FeedReport) raises -> String:
    return (
        "seen "
        + String(report.seen)
        + ", "
        + String(report.new)
        + " new, "
        + String(report.versions)
        + " versions, "
        + String(report.retracted)
        + " retracted, "
        + String(report.unchanged)
        + " unchanged"
    )


def apply_read(
    mut store: IndexStore,
    name: String,
    got: FeedRead,
    now_iso: String,
    seconds: Float64,
) raises -> FeedReport:
    """Write one feed's read in one transaction: its records, its retractions when the read
    saw the whole source, and its cursor. The cursor moves only with the records it covers.
    """
    var report = FeedReport(
        name,
        got.seen,
        0,
        0,
        0,
        0,
        seconds,
        got.cursor.copy(),
        got.note.copy(),
    )
    store.begin()
    try:
        for rec in got.records:
            var code = put_record(store, rec)
            if code == 1:
                report.new += 1
            elif code == 2:
                report.versions += 1
            else:
                report.unchanged += 1
        if got.complete:
            report.retracted = retract_missing(
                store, name, got.all_ids, now_iso
            )
        set_feed(store, name, got.cursor, now_iso, summary_of(report))
    except err:
        store.rollback()
        raise Error(String(err))
    store.commit()
    return report^


def run_git(
    mut store: IndexStore, cfg: FeedConfig, now_iso: String
) raises -> FeedReport:
    var started = now_seconds()
    var reading = read_git(
        cfg.git_path, cfg.git_branch, feed_cursor(store, GIT_FEED)
    )
    return apply_read(
        store, GIT_FEED, reading, now_iso, now_seconds() - started
    )


def run_vaults(
    mut store: IndexStore, cfg: FeedConfig, now_iso: String
) raises -> FeedReport:
    var started = now_seconds()
    var reading = read_vaults(
        cfg.vault_roots,
        cfg.vault_exclude,
        cfg.vault_max_bytes,
        feed_cursor(store, VAULTS_FEED),
    )
    return apply_read(
        store, VAULTS_FEED, reading, now_iso, now_seconds() - started
    )


def run_journal(
    mut store: IndexStore, journal: String, home: String, now_iso: String
) raises -> FeedReport:
    """The journal feed is the index's own journal path (index_build.mojo). Its cursor is
    kept in meta (the offset and the prefix hash), and mirrored in the feeds table here.
    """
    var started = now_seconds()
    var built: BuildReport = update_from_journal(store, journal, home)
    var cursor = (
        store.meta_get("journal_offset")
        + " "
        + store.meta_get("journal_prefix")
    )
    var report = FeedReport(
        JOURNAL_FEED,
        built.lines,
        built.written - built.versions,
        built.versions,
        0,
        built.unchanged,
        now_seconds() - started,
        cursor.copy(),
        String(),
    )
    if built.rebuilt:
        report.note = "the journal was rewritten, so its facts were read again"
    if built.missing_bodies > 0:
        if report.note.byte_length() > 0:
            report.note += "; "
        report.note += (
            String(built.missing_bodies) + " read bodies missing from docs/"
        )
    store.begin()
    try:
        set_feed(store, JOURNAL_FEED, cursor, now_iso, summary_of(report))
    except err:
        store.rollback()
        raise Error(String(err))
    store.commit()
    return report^


def run_feed(
    mut store: IndexStore,
    name: String,
    cfg: FeedConfig,
    journal: String,
    home: String,
    now_iso: String,
) raises -> FeedReport:
    if name == JOURNAL_FEED:
        return run_journal(store, journal, home, now_iso)
    if name == GIT_FEED:
        return run_git(store, cfg, now_iso)
    return run_vaults(store, cfg, now_iso)


def reset_feed(mut store: IndexStore, name: String) raises:
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
    except err:
        store.rollback()
        raise Error(String(err))
    store.commit()
