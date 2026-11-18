# The feed command: `vinci feed run [journal|git|vaults ...]`, `vinci feed status`, and
# `vinci feed rebuild <journal|git|vaults>`, with --index PATH on each. All feeds run when no
# name is given. Settings are read from $VINCI_HOME/feeds.toml (feed.mojo). None of the
# commands writes the journal.

from std.pathlib import Path

from feed import (
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
from feed_store import count_current, count_versions, feed_cursor, feed_row
from index_store import IndexStore
from open_view import utf8_prefix
from session import user_error
from text import pad_right
from timeutil import Instant, format_instant

comptime INDEX_FILE = "index.sqlite"
comptime USAGE = (
    "usage: vinci feed run [journal|git|vaults ...] | status |"
    " rebuild <journal|git|vaults> [--index PATH]"
)
comptime SHORT_CURSOR_CHARS = 24


def join_path(dir: String, name: String) -> String:
    if dir.endswith("/"):
        return dir + name
    return dir + "/" + name


def short(text: String) raises -> String:
    """The first SHORT_CURSOR_CHARS bytes of a cursor, for a one-line report."""
    if text.byte_length() <= SHORT_CURSOR_CHARS:
        return text.copy()
    return utf8_prefix(text, SHORT_CURSOR_CHARS) + "..."


def cursor_note(name: String, cursor: String) raises -> String:
    """What a report says about the cursor it left: one line, never the vault listing.
    """
    if name == JOURNAL_FEED:
        return "cursor " + short(cursor)
    if name == GIT_FEED:
        return "cursor " + short(cursor)
    var files = 0
    for line in cursor.split("\n"):
        if String(line).byte_length() > 0:
            files += 1
    return String(files) + " files in the cursor"


def report_line(report: FeedReport) raises -> String:
    var millis = Int(report.seconds * 1000)
    var line = (
        report.name + ": " + summary_of(report) + ", " + String(millis) + " ms"
    )
    if report.note.byte_length() > 0:
        line += " (" + report.note + ")"
    return line + "\n  " + cursor_note(report.name, report.cursor)


def feed_enabled(name: String, cfg: FeedConfig) -> Bool:
    if name == GIT_FEED:
        return cfg.git_enabled
    if name == VAULTS_FEED:
        return cfg.vault_enabled
    return True


def run_command(
    mut store: IndexStore,
    names: List[String],
    cfg: FeedConfig,
    journal: String,
    home: String,
    now_iso: String,
) raises:
    var chosen = List[String]()
    var explicit = len(names) > 0
    if explicit:
        for name in names:
            chosen.append(name.copy())
    else:
        chosen.append(JOURNAL_FEED)
        chosen.append(GIT_FEED)
        chosen.append(VAULTS_FEED)

    var failed = List[String]()
    for name in chosen:
        if not known_feed(name):
            raise user_error(
                "no feed named '" + name + "' (journal, git, vaults)"
            )
        if not feed_enabled(name, cfg):
            if explicit:
                raise user_error(name + " is turned off in " + CONFIG_FILE)
            print(name + ": off in " + CONFIG_FILE)
            continue
        try:
            var report = run_feed(store, name, cfg, journal, home, now_iso)
            print(report_line(report))
        except err:
            print(
                name
                + ": failed, and nothing it read was written: "
                + String(err)
            )
            failed.append(name.copy())
    if len(failed) > 0:
        var joined = String()
        for i in range(len(failed)):
            if i > 0:
                joined += ", "
            joined += failed[i]
        raise user_error("feed run failed for " + joined)


def status_command(store: IndexStore, cfg: FeedConfig) raises:
    for name in [JOURNAL_FEED, GIT_FEED, VAULTS_FEED]:
        if not feed_enabled(name, cfg):
            print(pad_right(name, 9) + "off in " + CONFIG_FILE)
            continue
        var cursor_text: String
        if name == JOURNAL_FEED:
            cursor_text = (
                "offset "
                + store.meta_get("journal_offset")
                + " ("
                + store.meta_get("journal_lines")
                + " lines)"
            )
        else:
            var cursor = feed_cursor(store, name)
            if name == GIT_FEED:
                cursor_text = (
                    short(cursor) if cursor.byte_length() > 0 else "no cursor"
                )
            else:
                cursor_text = cursor_note(name, cursor)
        var last = String("never run")
        var row = feed_row(store, name)
        if row is not None:
            var stored = row.value().copy()
            last = "last run " + stored[0] + ": " + stored[1]
        print(
            pad_right(name, 9)
            + cursor_text
            + "; "
            + String(count_current(store, name))
            + " current, "
            + String(count_versions(store, name))
            + " versions; "
            + last
        )


def feed_command(
    args: List[String], home: String, journal: String, now: Instant
) raises:
    var index_path = join_path(home, INDEX_FILE)
    var positional = List[String]()
    var i = 0
    while i < len(args):
        if args[i] == "--index":
            if i + 1 >= len(args):
                raise user_error("--index needs a path")
            index_path = args[i + 1].copy()
            i += 2
            continue
        positional.append(args[i].copy())
        i += 1
    if len(positional) == 0:
        raise user_error(USAGE)
    var sub = positional[0].copy()
    var names = List[String]()
    for k in range(1, len(positional)):
        names.append(positional[k].copy())

    var cfg = load_config(join_path(home, CONFIG_FILE))
    if sub == "status":
        if not Path(index_path).exists():
            raise user_error(
                "no index at " + index_path + "; run vinci feed run"
            )
        var status_store = IndexStore(index_path)
        status_command(status_store, cfg)
        return
    if sub == "run":
        var run_store = IndexStore(index_path)
        run_command(run_store, names, cfg, journal, home, format_instant(now))
        return
    if sub == "rebuild":
        if len(names) != 1:
            raise user_error("usage: vinci feed rebuild <journal|git|vaults>")
        var name = names[0].copy()
        if not known_feed(name):
            raise user_error(
                "no feed named '" + name + "' (journal, git, vaults)"
            )
        if not feed_enabled(name, cfg):
            raise user_error(name + " is turned off in " + CONFIG_FILE)
        var rebuild_store = IndexStore(index_path)
        reset_feed(rebuild_store, name)
        print("rebuilding " + name + " from the start")
        var report = run_feed(
            rebuild_store, name, cfg, journal, home, format_instant(now)
        )
        print(report_line(report))
        return
    raise user_error("unknown feed command '" + sub + "'. " + USAGE)
