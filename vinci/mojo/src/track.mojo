# The window and git tracker: a port of src/Tracker.cs. Program.cs runs it before every
# command that App.Run dispatches (not serve, and not the index, recall, open or feed
# commands, which the C# never had). It credits the window time since the last sync as
# "spent" facts dated when that time ended, at most one "used" fact a day for what was
# shipped, and commits under an idea's home as "work" facts, then writes one "sync" fact.
# `vinci sync` prints the lines it wrote.
#
# The reads from outside (ActivityWatch's query, git, the machine's UTC offset, the case
# folding OrdinalIgnoreCase uses) are in py/vinci_edge/track_edge.py. A failed read is the
# C#'s null: no window time is added and the sync line says so, but commits are still read.

from std.math import floor
from std.pathlib import Path
from std.python import Python, PythonObject

from embed_edge import edge_module
from journal import Event, JournalTx
from state import RETIRED, State, Watch, state_from
from text import day, pad_right, span, utf16_length
from timeutil import (
    DAYS_0001_TO_1970,
    TICKS_PER_DAY,
    TICKS_PER_HOUR,
    TICKS_PER_MINUTE,
    TICKS_PER_SECOND,
    Instant,
    ascii_prefix,
    civil_from_days,
    parse_instant,
    zero_pad,
)

# Tracker.NothingWatched, shown by sync and by watches when nothing is watched.
comptime NOTHING_WATCHED = (
    "Nothing is watched. vinci home <idea> <folder>, or vinci watch <idea>"
    ' "text in its window titles"'
)
comptime NO_ANSWER = (
    "ActivityWatch did not answer, so no window time was added. It will be"
    " picked up next time."
)
# With nothing to credit, the place reached is still written down once a day.
comptime MARK_EVERY = TICKS_PER_DAY
# A glance at a window is not use. This much time in it, in one sync, is.
comptime USE_AT_LEAST = TICKS_PER_MINUTE


@fieldwise_init
struct Activity(Copyable, Movable):
    """One stretch the watcher had a window in focus, with the window text folded for matching.
    """

    var start: Instant
    var length: Int  # ticks
    var window: String


@fieldwise_init
struct Seen(Copyable, Movable):
    """What the watcher reported for a period. answered is False when the read failed,
    which is the C# null."""

    var answered: Bool
    var activities: List[Activity]
    var through: Instant


@fieldwise_init
struct Commit(Copyable, Movable):
    """One commit, the time it was made, its subject, and the full paths it changed.
    """

    var hash: String
    var at: Instant
    var subject: String
    var files: List[String]


@fieldwise_init
struct Found(Copyable, Movable):
    """A commit that counts as work on an idea, by index into the state's ideas.
    """

    var idea: Int
    var commit: Commit


@fieldwise_init
struct Credit(Copyable, Movable):
    """The time credited to one watch target this sync, and where it last ended.
    """

    var idea: Int
    var use: Bool
    var total: Int  # ticks
    var last: Instant


@fieldwise_init
struct Compose(Movable):
    """What one pass of the tracker produces: the lines to print and the facts to write.
    """

    var lines: List[String]
    var events: List[Event]


struct TrackReads(Movable):
    """The external reads one tracker sync makes, keyed by what they ask for. A read is made
    once per key, so the pass that decides whether to write and the pass that writes (under
    the lock) share them, and the lock is never held for a read it could have made before.
    """

    var seen_keys: List[String]
    var seen: List[Seen]
    var commit_keys: List[String]
    var commits: List[List[Commit]]

    def __init__(out self):
        self.seen_keys = List[String]()
        self.seen = List[Seen]()
        self.commit_keys = List[String]()
        self.commits = List[List[Commit]]()


def read_seen(
    mut reads: TrackReads, server: String, start: Instant, end: Instant
) raises -> Seen:
    """fetch_seen, made at most once for the same server and period."""
    var key = server + "|" + String(start.utc) + "|" + String(end.utc)
    for i in range(len(reads.seen_keys)):
        if reads.seen_keys[i] == key:
            return reads.seen[i].copy()
    var seen = fetch_seen(server, start.copy(), end.copy())
    reads.seen_keys.append(key)
    reads.seen.append(seen.copy())
    return seen^


def read_commits(
    mut reads: TrackReads, folder: String, after: Optional[Instant]
) raises -> List[Commit]:
    """git_commits, made at most once for the same folder and starting moment.
    """
    var when = String("latest")
    if after:
        when = String(after.value().utc)
    var key = folder + "|" + when
    for i in range(len(reads.commit_keys)):
        if reads.commit_keys[i] == key:
            return reads.commits[i].copy()
    var found = git_commits(folder, after)
    reads.commit_keys.append(key)
    reads.commits.append(found.copy())
    return found^


def activitywatch_server() raises -> String:
    """VINCI_ACTIVITYWATCH, or the default server, as Program.cs reads it."""
    var os = Python.import_module("os")
    var value = os.environ.get(PythonObject("VINCI_ACTIVITYWATCH"))
    if Bool(value == Python.none()):
        return String("http://127.0.0.1:5600")
    return String(value)


def track_edge() raises -> PythonObject:
    """vinci_edge.track_edge, with mojo/py on sys.path (edge_module puts it there).
    """
    _ = edge_module()
    return Python.import_module("vinci_edge.track_edge")


def py_strings(items: List[String]) raises -> PythonObject:
    """A Python list of strings, in order."""
    var out = Python.list()
    for item in items:
        out.append(PythonObject(item))
    return out


def combine(folder: String, name: String) -> String:
    """Path.Combine for a relative name on Linux."""
    if folder.endswith("/"):
        return folder + name
    return folder + "/" + name


def parent_of(folder: String) -> Optional[String]:
    """Path.GetDirectoryName for an absolute path: None at the root."""
    var src = folder.as_bytes()
    var cut = -1
    for i in range(len(src)):
        if src[i] == 47:  # /
            cut = i
    if cut < 0:
        return None
    if cut == 0:
        if len(src) == 1:
            return None
        return String("/")
    return ascii_prefix(folder, cut)


def repository_of(folder: String) raises -> Optional[String]:
    """RepositoryOf: the nearest folder at or above this one that holds a .git.
    """
    var dir = folder
    while True:
        if Path(combine(dir, ".git")).exists():
            return dir
        var parent = parent_of(dir)
        if not parent:
            return None
        dir = parent.value()


def inside(file: String, folder: String) -> Bool:
    """Tracker.Inside: the file is under the folder, by a whole path segment."""
    return file.startswith(combine(folder, ""))


def contains_string(items: List[String], want: String) -> Bool:
    for item in items:
        if item == want:
            return True
    return False


def contains_int(items: List[Int], want: Int) -> Bool:
    for item in items:
        if item == want:
            return True
    return False


def byte_slice(text: String, start: Int, end: Int) -> String:
    """The bytes from start up to end. Only used at ASCII separators, so it stays UTF-8.
    """
    var src = text.as_bytes()
    var out = List[UInt8]()
    for i in range(start, end):
        out.append(src[i])
    return String(unsafe_from_utf8=out)


def split_byte(text: String, sep: UInt8) -> List[String]:
    """String.Split on one ASCII byte, keeping the empty pieces."""
    var src = text.as_bytes()
    var out = List[String]()
    var start = 0
    for i in range(len(src)):
        if src[i] == sep:
            out.append(byte_slice(text, start, i))
            start = i + 1
    out.append(byte_slice(text, start, len(src)))
    return out^


def find_byte(text: String, sep: UInt8, from_at: Int) -> Int:
    """The index of the first sep byte at or after from_at, or -1."""
    var src = text.as_bytes()
    for i in range(from_at, len(src)):
        if src[i] == sep:
            return i
    return -1


def unix_seconds(at: Instant) -> Int:
    """DateTimeOffset.ToUnixTimeSeconds: whole seconds since 1970, for a time after 1970.
    """
    return (at.utc - DAYS_0001_TO_1970 * TICKS_PER_DAY) // TICKS_PER_SECOND


def local_at(at: Instant) raises -> Instant:
    """ToLocalTime(): the same moment in the machine's own UTC offset."""
    var pair = track_edge().local_offset(PythonObject(unix_seconds(at)))
    if String(pair[0]) != "ok":
        raise Error("track: no local offset: " + String(pair[1]))
    return Instant(at.utc, Int(String(pair[1])))


def local_minute(at: Instant) raises -> String:
    """ToLocalTime():yyyy-MM-dd HH:mm, as the sync line of an empty period shows it.
    """
    var local = local_at(at)
    var ticks = local.local_ticks()
    var rest = ticks - (ticks // TICKS_PER_DAY) * TICKS_PER_DAY
    var hour = rest // TICKS_PER_HOUR
    var minute = (rest // TICKS_PER_MINUTE) % 60
    return day(local) + " " + zero_pad(hour, 2) + ":" + zero_pad(minute, 2)


def utc_o(at: Instant) -> String:
    """DateTime.UtcDateTime.ToString("O"): seven fraction digits, always, then Z.

    The request's period is written this way, and the probe showed .NET keeps the zeros.
    """
    var ticks = at.utc
    var days = ticks // TICKS_PER_DAY
    var rest = ticks - days * TICKS_PER_DAY
    var date = civil_from_days(days - DAYS_0001_TO_1970)
    var hour = rest // TICKS_PER_HOUR
    var minute = (rest // TICKS_PER_MINUTE) % 60
    var second = (rest // TICKS_PER_SECOND) % 60
    var fraction = rest % TICKS_PER_SECOND
    return (
        zero_pad(date.year, 4)
        + "-"
        + zero_pad(date.month, 2)
        + "-"
        + zero_pad(date.day, 2)
        + "T"
        + zero_pad(hour, 2)
        + ":"
        + zero_pad(minute, 2)
        + ":"
        + zero_pad(second, 2)
        + "."
        + zero_pad(fraction, 7)
        + "Z"
    )


def round_minutes(minutes: Float64) -> Float64:
    """Math.Round(minutes, 2): the scaled value rounds to even on a half, as .NET does.
    """
    var scaled = minutes * 100.0
    var low = floor(scaled)
    var fraction = scaled - low
    var pick = low
    if fraction > 0.5 or (fraction == 0.5 and Int(low) % 2 != 0):
        pick = low + 1.0
    return pick / 100.0


def ordered_watches(s: State) raises -> List[Watch]:
    """AllWatches, most specific first: the longest pattern (in UTF-16 units) first, and
    the order kept for equal lengths (OrderByDescending is stable)."""
    var out = List[Watch]()
    for w in s.all_watches():
        var key = utf16_length(w.pattern)
        var at = len(out)
        for i in range(len(out)):
            if utf16_length(out[i].pattern) < key:
                at = i
                break
        out.insert(at, w.copy())
    return out^


def fetch_seen(server: String, start: Instant, end: Instant) raises -> Seen:
    """ActivityWatch.At: the windows and the afk record for one period, or not answered.

    Every failure is not answered: a refused connection, a non-2xx reply, a body that is
    not the expected JSON, and a timestamp .NET would not read.
    """
    var pair = track_edge().activity(
        PythonObject(server),
        PythonObject(utc_o(start)),
        PythonObject(utc_o(end)),
    )
    if String(pair[0]) != "ok":
        return Seen(False, List[Activity](), start.copy())
    try:
        var reply = pair[1]
        var activities = List[Activity]()
        for row in reply[0]:
            activities.append(
                Activity(
                    parse_instant(String(row[0])),
                    Int(String(row[1])),
                    String(row[2]),
                )
            )
        var through = start.copy()
        for row in reply[1]:
            var row_end = parse_instant(String(row[0])).add_ticks(
                Int(String(row[1]))
            )
            if row_end.utc > through.utc:
                through = row_end.copy()
        return Seen(True, activities^, through^)
    except:
        return Seen(False, List[Activity](), start.copy())


def credit_windows(
    mut reads: TrackReads, s: State, server: String, now: Instant
) raises -> Compose:
    """Tracker.Windows: credit the window time since the last sync, and the sync line.

    Returns the lines to print for it (or the not-answered and nothing-watched lines) and the
    facts to write. Nothing is written here: the caller writes them under the lock.
    """
    var events = List[Event]()
    var ordered = ordered_watches(s)
    var patterns = List[String]()
    for w in ordered:
        patterns.append(w.pattern.copy())
    var from_at: Instant
    if s.synced_to:
        from_at = s.synced_to.value().copy()
    else:
        from_at = s.watching_since.value().copy()

    var seen = read_seen(reads, server, from_at, now)
    if not seen.answered:
        var none = List[String]()
        none.append(NO_ANSWER)
        return Compose(none^, events^)
    var to = seen.through.copy() if seen.through.utc < now.utc else now.copy()

    # Matching is OrdinalIgnoreCase, so both sides are folded before the search.
    var raw_windows = List[String]()
    for a in seen.activities:
        raw_windows.append(a.window.copy())
    var folded_windows = track_fold(raw_windows)
    var folded_patterns = track_fold(patterns)
    var windows = List[Activity]()
    for i in range(len(seen.activities)):
        windows.append(
            Activity(
                seen.activities[i].start.copy(),
                seen.activities[i].length,
                folded_windows[i].copy(),
            )
        )

    var credits = List[Credit]()
    for a in windows:
        var start = (
            a.start.copy() if a.start.utc > from_at.utc else from_at.copy()
        )
        var a_end = a.start.add_ticks(a.length)
        var end = a_end.copy() if a_end.utc < to.utc else to.copy()
        if end.utc <= start.utc:
            continue
        # The most specific watch wins when several match the same window.
        var hit = -1
        for k in range(len(ordered)):
            if folded_patterns[k] in a.window:
                hit = k
                break
        if hit < 0:
            continue
        var idea = s.idea_index(ordered[hit].idea_id)
        var use = ordered[hit].use
        var ticks = end.utc - start.utc
        var found = -1
        for c in range(len(credits)):
            if credits[c].idea == idea and credits[c].use == use:
                found = c
                break
        if found < 0:
            credits.append(Credit(idea, use, ticks, end.copy()))
        else:
            var merged = credits[found].copy()
            merged.total += ticks
            if end.utc > merged.last.utc:
                merged.last = end.copy()
            credits[found] = merged^

    var lines = List[String]()
    for c in credits:
        var idea = s.ideas[c.idea].copy()
        if c.use:
            if len(idea.ships) == 0 or c.total < USE_AT_LEAST:
                continue
            var last_day = day(local_at(c.last))
            var used_today = False
            for u in idea.uses:
                if day(local_at(u)) == last_day:
                    used_today = True
            if used_today:
                continue
            events.append(
                Event(
                    c.last.copy(),
                    "used",
                    id=idea.id.copy(),
                    text=span(c.total) + " on screen",
                    where_=String("window"),
                )
            )
            lines.append(pad_right(idea.id, 4) + " used  " + idea.title)
            continue
        var minutes = round_minutes(
            Float64(c.total) / Float64(TICKS_PER_MINUTE)
        )
        if minutes <= 0.0:
            continue
        events.append(
            Event(
                c.last.copy(),
                "spent",
                id=idea.id.copy(),
                value=minutes,
                where_=String("window"),
            )
        )
        lines.append(
            pad_right(idea.id, 4) + " +" + span(c.total) + "  " + idea.title
        )

    if len(credits) > 0 or to.utc - from_at.utc > MARK_EVERY:
        events.append(Event(to.copy(), "sync"))
    if len(lines) > 0:
        return Compose(lines^, events^)
    var empty = List[String]()
    empty.append("No watched window since " + local_minute(from_at) + ".")
    return Compose(empty^, events^)


def track_fold(texts: List[String]) raises -> List[String]:
    """OrdinalIgnoreCase's folding, done by the edge in one call for the whole list.
    """
    var pair = track_edge().fold_upper(py_strings(texts))
    var out = List[String]()
    for item in pair[1]:
        out.append(String(item))
    return out^


def git_commits(
    folder: String, after: Optional[Instant]
) raises -> List[Commit]:
    """Tracker.Log: the commits that changed something under folder, oldest first.

    With after, the commits since that moment; without it, the latest one.
    """
    var arg: PythonObject
    if after:
        arg = PythonObject(unix_seconds(after.value()))
    else:
        arg = Python.none()
    var pair = track_edge().git_log(PythonObject(folder), arg)
    var commits = List[Commit]()
    if String(pair[0]) != "ok":
        return commits^
    var text = String(pair[1])
    for block in split_byte(text, 1):  # \x01 starts each commit
        var rows = List[String]()
        for row in split_byte(block, 10):  # \n
            if row.byte_length() > 0:
                rows.append(row.copy())
        if len(rows) == 0:
            continue
        var head = rows[0].copy()
        var first_tab = find_byte(head, 9, 0)
        var second_tab = find_byte(head, 9, first_tab + 1)
        if first_tab < 0 or second_tab < 0:
            raise Error("track: a git log header has no tabs")
        var files = List[String]()
        for k in range(1, len(rows)):
            files.append(combine(folder, rows[k]))
        commits.append(
            Commit(
                byte_slice(head, 0, first_tab),
                parse_instant(byte_slice(head, first_tab + 1, second_tab)),
                byte_slice(head, second_tab + 1, head.byte_length()),
                files^,
            )
        )
    return commits^


def commit_sync(mut reads: TrackReads, s: State) raises -> Compose:
    """Tracker.Commits: a commit under an idea's home is work on that idea.

    An idea with no work yet takes its latest commit only. Every other homed idea takes the
    commits since the newest work recorded on any of them, from one question per repository.
    A commit goes to the deepest home that holds one of its files.
    """
    var homed = List[Int]()
    for i in range(len(s.ideas)):
        if s.ideas[i].home and s.ideas[i].stage != RETIRED:
            homed.append(i)
    var lines = List[String]()
    var events = List[Event]()
    if len(homed) == 0:
        return Compose(lines^, events^)

    var found = List[Found]()
    for i in homed:
        if len(s.ideas[i].work) == 0:
            for c in read_commits(reads, s.ideas[i].home.value(), None):
                found.append(Found(i, c.copy()))

    var has_since = False
    var since = Instant(0, 0)
    for i in homed:
        for w in s.ideas[i].work:
            if not has_since or w.at.utc > since.utc:
                since = w.at.copy()
                has_since = True
    if has_since:
        var roots = List[String]()
        for i in homed:
            if len(s.ideas[i].work) > 0:
                var root = repository_of(s.ideas[i].home.value())
                if root and not contains_string(roots, root.value()):
                    roots.append(root.value())
        for root in roots:
            for commit in read_commits(
                reads, root, Optional[Instant](since.copy())
            ):
                var taken = List[Int]()
                for file in commit.files:
                    var best = -1
                    for i in homed:
                        if len(s.ideas[i].work) == 0:
                            continue
                        var home = s.ideas[i].home.value()
                        if not inside(file, home):
                            continue
                        if (
                            best < 0
                            or home.byte_length()
                            > s.ideas[best].home.value().byte_length()
                        ):
                            best = i
                    if best >= 0 and not contains_int(taken, best):
                        taken.append(best)
                        found.append(Found(best, commit.copy()))

    var groups = List[Int]()
    for f in found:
        if s.has_commit(s.ideas[f.idea].id, f.commit.hash):
            continue
        if not contains_int(groups, f.idea):
            groups.append(f.idea)
    for g in groups:
        var idea = s.ideas[g].copy()
        var members = List[Commit]()
        for f in found:
            if f.idea != g or s.has_commit(idea.id, f.commit.hash):
                continue
            # OrderBy on the commit time: a stable insert, oldest first.
            var at = len(members)
            for k in range(len(members)):
                if members[k].at.utc > f.commit.at.utc:
                    at = k
                    break
            members.insert(at, f.commit.copy())
        for m in members:
            events.append(
                Event(
                    m.at.copy(),
                    "work",
                    id=idea.id.copy(),
                    text=m.subject.copy(),
                    where_=m.hash.copy(),
                )
            )
        var plural = String("") if len(members) == 1 else String("s")
        lines.append(
            pad_right(idea.id, 4)
            + " "
            + String(len(members))
            + " commit"
            + plural
            + "  "
            + idea.title
        )
    return Compose(lines^, events^)


def compose(
    mut reads: TrackReads, s: State, server: String, now: Instant
) raises -> Compose:
    """The window credits, then the commits: the lines to print and the facts to write, in
    the order they are written. Reads go through reads, so a second call with the same state
    makes no new external read."""
    var credit = credit_windows(reads, s, server, now)
    var commits = commit_sync(reads, s)
    var lines = credit.lines.copy()
    for line in commits.lines:
        lines.append(line.copy())
    var events = credit.events.copy()
    for e in commits.events:
        events.append(e.copy())
    return Compose(lines^, events^)


def track_sync(
    journal_path: String, server: String, now: Instant
) raises -> List[String]:
    """Tracker.Sync: credit the windows, then the commits, and return the lines to print.

    The external reads (ActivityWatch, git) happen first, with no lock: readers never take
    the lock, and a sync that has nothing to write takes none. The state is folded once
    without the lock to see whether anything is due. Only then does it take the lock,
    fold the state again under it, and write the facts in one batch with one fsync. The reads
    from the first pass are reused, so a write made in between by another process is seen
    in the state but not read again (nothing is fetched under the lock unless the state's
    sync point moved, in which case the read is made under the lock, as before).
    """
    var s0 = state_from(JournalTx(journal_path).read())
    var out = List[String]()
    if len(s0.all_watches()) == 0:
        out.append(NOTHING_WATCHED)
        return out^
    var reads = TrackReads()
    var first = compose(reads, s0, server, now)
    var due = len(first.events) > 0
    var first_lines = first.lines.copy()
    if not due:
        return first_lines^

    var tx = JournalTx(journal_path)
    tx.begin()
    try:
        var s1 = state_from(tx.read())
        if len(s1.all_watches()) == 0:
            tx.abort()
            out.append(NOTHING_WATCHED)
            return out^
        var fresh = compose(reads, s1, server, now)
        var fresh_lines = fresh.lines.copy()
        var fresh_events = fresh.events.copy()
        for e in fresh_events:
            tx.append(e.copy())
        tx.commit()
        return fresh_lines^
    except err:
        tx.abort()
        raise err^
