"""The window and git tracker. It runs before every journal command (not serve, and not the
index, recall, open or feed commands). It credits the window time since the last sync as
"spent" facts dated when that time ended, at most one "used" fact a day for what was shipped,
and commits under an idea's home as "work" facts, then writes one "sync" fact. `vinci journal sync`
prints the lines it wrote.

The reads from outside (ActivityWatch's query, git, the machine's UTC offset, the case folding)
are in vinci_edge/track_edge.py. A failed read is "not answered": no window time is added and
the sync line says so, but commits are still read.

Port of mojo/src/track.mojo.
"""

from __future__ import annotations

import math
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional

from vinci_edge import track_edge

from .journal import Event, JournalTx
from .state import RETIRED, Idea, State, Watch, state_from
from .text import day, pad_right, span, utf16_length
from .timeutil import (
    DAYS_0001_TO_1970,
    TICKS_PER_DAY,
    TICKS_PER_HOUR,
    TICKS_PER_MINUTE,
    TICKS_PER_SECOND,
    Instant,
    civil_from_days,
    parse_instant,
    zero_pad,
)

# Shown by sync and by watches when nothing is watched.
NOTHING_WATCHED = (
    "Nothing is watched. vinci journal home <idea> <folder>, or vinci journal watch <idea>"
    ' "text in its window titles"'
)
NO_ANSWER = (
    "ActivityWatch did not answer, so no window time was added. It will be"
    " picked up next time."
)
# With nothing to credit, the place reached is still written down once a day.
MARK_EVERY = TICKS_PER_DAY
# A glance at a window is not use. This much time in it, in one sync, is.
USE_AT_LEAST = TICKS_PER_MINUTE


@dataclass
class Activity:
    """One stretch the watcher had a window in focus, with the window text folded for
    matching."""

    start: Instant
    length: int  # ticks
    window: str


@dataclass
class Seen:
    """What the watcher reported for a period. answered is False when the read failed."""

    answered: bool
    activities: list[Activity]
    through: Instant


@dataclass
class Commit:
    """One commit, the time it was made, its subject, and the full paths it changed."""

    hash: str
    at: Instant
    subject: str
    files: list[str]


@dataclass
class Found:
    """A commit that counts as work on an idea, by index into the state's ideas."""

    idea: int
    commit: Commit


@dataclass
class Credit:
    """The time credited to one watch target this sync, and where it last ended."""

    idea: int
    use: bool
    total: int  # ticks
    last: Instant


@dataclass
class Compose:
    """What one pass of the tracker produces: the lines to print and the facts to write."""

    lines: list[str]
    events: list[Event]


@dataclass
class TrackReads:
    """The external reads one tracker sync makes, keyed by what they ask for. A read is made
    once per key, so the pass that decides whether to write and the pass that writes (under
    the lock) share them."""

    seen: dict[str, Seen] = field(default_factory=dict)
    commits: dict[str, list[Commit]] = field(default_factory=dict)


def read_seen(reads: TrackReads, server: str, start: Instant, end: Instant) -> Seen:
    """fetch_seen, made at most once for the same server and period."""
    key = server + "|" + str(start.utc) + "|" + str(end.utc)
    if key not in reads.seen:
        reads.seen[key] = fetch_seen(server, start, end)
    return reads.seen[key]


def read_commits(reads: TrackReads, folder: str, after: Optional[Instant]) -> list[Commit]:
    """git_commits, made at most once for the same folder and starting moment."""
    when = "latest" if after is None else str(after.utc)
    key = folder + "|" + when
    if key not in reads.commits:
        reads.commits[key] = git_commits(folder, after)
    return reads.commits[key]


def activitywatch_server() -> str:
    """VINCI_ACTIVITYWATCH, or the default server."""
    value = os.environ.get("VINCI_ACTIVITYWATCH")
    if value is None:
        return "http://127.0.0.1:5600"
    return value


def combine(folder: str, name: str) -> str:
    """Path.Combine for a relative name on Linux."""
    if folder.endswith("/"):
        return folder + name
    return folder + "/" + name


def parent_of(folder: str) -> Optional[str]:
    """Path.GetDirectoryName for an absolute path: None at the root."""
    cut = folder.rfind("/")
    if cut < 0:
        return None
    if cut == 0:
        if len(folder) == 1:
            return None
        return "/"
    return folder[:cut]


def repository_of(folder: str) -> Optional[str]:
    """The nearest folder at or above this one that holds a .git."""
    directory = folder
    while True:
        if Path(combine(directory, ".git")).exists():
            return directory
        parent = parent_of(directory)
        if parent is None:
            return None
        directory = parent


def inside(file: str, folder: str) -> bool:
    """The file is under the folder, by a whole path segment."""
    return file.startswith(combine(folder, ""))


def unix_seconds(at: Instant) -> int:
    """DateTimeOffset.ToUnixTimeSeconds: whole seconds since 1970, for a time after 1970."""
    return (at.utc - DAYS_0001_TO_1970 * TICKS_PER_DAY) // TICKS_PER_SECOND


def local_at(at: Instant) -> Instant:
    """ToLocalTime(): the same moment in the machine's own UTC offset."""
    status, minutes = track_edge.local_offset(unix_seconds(at))
    if status != "ok":
        raise ValueError("track: no local offset: " + str(minutes))
    return Instant(at.utc, int(minutes))


def local_minute(at: Instant) -> str:
    """ToLocalTime():yyyy-MM-dd HH:mm, as the sync line of an empty period shows it."""
    local = local_at(at)
    ticks = local.local_ticks()
    rest = ticks - (ticks // TICKS_PER_DAY) * TICKS_PER_DAY
    hour = rest // TICKS_PER_HOUR
    minute = (rest // TICKS_PER_MINUTE) % 60
    return day(local) + " " + zero_pad(hour, 2) + ":" + zero_pad(minute, 2)


def utc_o(at: Instant) -> str:
    """ISO 8601 in UTC: seven fraction digits, always, then Z. The request's period is written
    this way, and the zeros are kept."""
    ticks = at.utc
    days = ticks // TICKS_PER_DAY
    rest = ticks - days * TICKS_PER_DAY
    date = civil_from_days(days - DAYS_0001_TO_1970)
    hour = rest // TICKS_PER_HOUR
    minute = (rest // TICKS_PER_MINUTE) % 60
    second = (rest // TICKS_PER_SECOND) % 60
    fraction = rest % TICKS_PER_SECOND
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


def split_byte(text: str, sep: int) -> list[str]:
    """String.Split on one ASCII byte, keeping the empty pieces."""
    return text.split(chr(sep))


def round_minutes(minutes: float) -> float:
    """Minutes rounded to two decimals: the scaled value rounds to even on a half."""
    scaled = minutes * 100.0
    low = math.floor(scaled)
    fraction = scaled - low
    pick = float(low)
    if fraction > 0.5 or (fraction == 0.5 and int(low) % 2 != 0):
        pick = float(low) + 1.0
    return pick / 100.0


def ordered_watches(s: State) -> list[Watch]:
    """AllWatches, most specific first: the longest pattern (in UTF-16 units) first, and the
    order kept for equal lengths (OrderByDescending is stable)."""
    out: list[Watch] = []
    for w in s.all_watches():
        key = utf16_length(w.pattern)
        at = len(out)
        for i in range(len(out)):
            if utf16_length(out[i].pattern) < key:
                at = i
                break
        out.insert(at, w)
    return out


def fetch_seen(server: str, start: Instant, end: Instant) -> Seen:
    """The windows and the afk record for one period, or not answered.

    Every failure is not answered: a refused connection, a non-2xx reply, a body that is not
    the expected JSON, and a timestamp that does not parse.
    """
    status, reply = track_edge.activity(server, utc_o(start), utc_o(end))
    if status != "ok":
        return Seen(False, [], start)
    try:
        activities: list[Activity] = []
        for row in reply[0]:
            activities.append(Activity(parse_instant(str(row[0])), int(row[1]), str(row[2])))
        through = start
        for row in reply[1]:
            row_end = parse_instant(str(row[0])).add_ticks(int(row[1]))
            if row_end.utc > through.utc:
                through = row_end
        return Seen(True, activities, through)
    except Exception:
        return Seen(False, [], start)


def track_fold(texts: list[str]) -> list[str]:
    """OrdinalIgnoreCase's folding, done by the edge in one call for the whole list."""
    status, folded = track_edge.fold_upper(list(texts))
    return list(folded)


def credit_windows(reads: TrackReads, s: State, server: str, now: Instant) -> Compose:
    """Tracker.Windows: credit the window time since the last sync, and the sync line.

    Returns the lines to print for it (or the not-answered and nothing-watched lines) and the
    facts to write. Nothing is written here: the caller writes them under the lock.
    """
    events: list[Event] = []
    ordered = ordered_watches(s)
    patterns = [w.pattern for w in ordered]
    if s.synced_to is not None:
        from_at = s.synced_to
    else:
        from_at = s.watching_since  # type: ignore[assignment]

    seen = read_seen(reads, server, from_at, now)
    if not seen.answered:
        return Compose([NO_ANSWER], events)
    to = seen.through if seen.through.utc < now.utc else now

    # Matching is OrdinalIgnoreCase, so both sides are folded before the search.
    folded_windows = track_fold([a.window for a in seen.activities])
    folded_patterns = track_fold(patterns)
    windows = [
        Activity(seen.activities[i].start, seen.activities[i].length, folded_windows[i])
        for i in range(len(seen.activities))
    ]

    credits: list[Credit] = []
    for a in windows:
        start = a.start if a.start.utc > from_at.utc else from_at
        a_end = a.start.add_ticks(a.length)
        end = a_end if a_end.utc < to.utc else to
        if end.utc <= start.utc:
            continue
        # The most specific watch wins when several match the same window.
        hit = -1
        for k in range(len(ordered)):
            if folded_patterns[k] in a.window:
                hit = k
                break
        if hit < 0:
            continue
        idea = s.idea_index(ordered[hit].idea_id)
        use = ordered[hit].use
        ticks = end.utc - start.utc
        found = -1
        for c in range(len(credits)):
            if credits[c].idea == idea and credits[c].use == use:
                found = c
                break
        if found < 0:
            credits.append(Credit(idea, use, ticks, end))
        else:
            merged = credits[found]
            merged.total += ticks
            if end.utc > merged.last.utc:
                merged.last = end
            credits[found] = merged

    lines: list[str] = []
    for c in credits:
        idea: Idea = s.ideas[c.idea]
        if c.use:
            if len(idea.ships) == 0 or c.total < USE_AT_LEAST:
                continue
            last_day = day(local_at(c.last))
            used_today = any(day(local_at(u)) == last_day for u in idea.uses)
            if used_today:
                continue
            events.append(
                Event(
                    c.last,
                    "used",
                    id=idea.id,
                    text=span(c.total) + " on screen",
                    where_="window",
                )
            )
            lines.append(pad_right(idea.id, 4) + " used  " + idea.title)
            continue
        minutes = round_minutes(float(c.total) / float(TICKS_PER_MINUTE))
        if minutes <= 0.0:
            continue
        events.append(Event(c.last, "spent", id=idea.id, value=minutes, where_="window"))
        lines.append(pad_right(idea.id, 4) + " +" + span(c.total) + "  " + idea.title)

    if len(credits) > 0 or to.utc - from_at.utc > MARK_EVERY:
        events.append(Event(to, "sync"))
    if len(lines) > 0:
        return Compose(lines, events)
    return Compose(["No watched window since " + local_minute(from_at) + "."], events)


def git_commits(folder: str, after: Optional[Instant]) -> list[Commit]:
    """Tracker.Log: the commits that changed something under folder, oldest first.

    With after, the commits since that moment; without it, the latest one.
    """
    arg = None if after is None else unix_seconds(after)
    status, text = track_edge.git_log(folder, arg)
    commits: list[Commit] = []
    if status != "ok":
        return commits
    for block in text.split("\x01"):  # \x01 starts each commit
        rows = [row for row in block.split("\n") if len(row) > 0]
        if len(rows) == 0:
            continue
        head = rows[0]
        first_tab = head.find("\t")
        second_tab = head.find("\t", first_tab + 1) if first_tab >= 0 else -1
        if first_tab < 0 or second_tab < 0:
            raise ValueError("track: a git log header has no tabs")
        files = [combine(folder, row) for row in rows[1:]]
        commits.append(
            Commit(
                head[:first_tab],
                parse_instant(head[first_tab + 1 : second_tab]),
                head[second_tab + 1 :],
                files,
            )
        )
    return commits


def commit_sync(reads: TrackReads, s: State) -> Compose:
    """Tracker.Commits: a commit under an idea's home is work on that idea.

    An idea with no work yet takes its latest commit only. Every other homed idea takes the
    commits since the newest work recorded on any of them, from one question per repository.
    A commit goes to the deepest home that holds one of its files.
    """
    homed = [i for i, idea in enumerate(s.ideas) if idea.home is not None and idea.stage != RETIRED]
    lines: list[str] = []
    events: list[Event] = []
    if len(homed) == 0:
        return Compose(lines, events)

    found: list[Found] = []
    for i in homed:
        if len(s.ideas[i].work) == 0:
            home = s.ideas[i].home
            assert home is not None
            for c in read_commits(reads, home, None):
                found.append(Found(i, c))

    has_since = False
    since = Instant(0, 0)
    for i in homed:
        for w in s.ideas[i].work:
            if not has_since or w.at.utc > since.utc:
                since = w.at
                has_since = True
    if has_since:
        roots: list[str] = []
        for i in homed:
            if len(s.ideas[i].work) > 0:
                home = s.ideas[i].home
                assert home is not None
                root = repository_of(home)
                if root is not None and root not in roots:
                    roots.append(root)
        for root in roots:
            for commit in read_commits(reads, root, since):
                taken: list[int] = []
                for file in commit.files:
                    best = -1
                    for i in homed:
                        if len(s.ideas[i].work) == 0:
                            continue
                        home = s.ideas[i].home
                        assert home is not None
                        if not inside(file, home):
                            continue
                        if best < 0 or len(home.encode("utf-8")) > len((s.ideas[best].home or "").encode("utf-8")):
                            best = i
                    if best >= 0 and best not in taken:
                        taken.append(best)
                        found.append(Found(best, commit))

    groups: list[int] = []
    for f in found:
        if s.has_commit(s.ideas[f.idea].id, f.commit.hash):
            continue
        if f.idea not in groups:
            groups.append(f.idea)
    for g in groups:
        idea = s.ideas[g]
        members: list[Commit] = []
        for f in found:
            if f.idea != g or s.has_commit(idea.id, f.commit.hash):
                continue
            # OrderBy on the commit time: a stable insert, oldest first.
            at = len(members)
            for k in range(len(members)):
                if members[k].at.utc > f.commit.at.utc:
                    at = k
                    break
            members.insert(at, f.commit)
        for m in members:
            events.append(Event(m.at, "work", id=idea.id, text=m.subject, where_=m.hash))
        plural = "" if len(members) == 1 else "s"
        lines.append(pad_right(idea.id, 4) + " " + str(len(members)) + " commit" + plural + "  " + idea.title)
    return Compose(lines, events)


def compose(reads: TrackReads, s: State, server: str, now: Instant) -> Compose:
    """The window credits, then the commits: the lines to print and the facts to write, in
    the order they are written. Reads go through reads, so a second call with the same state
    makes no new external read."""
    credit = credit_windows(reads, s, server, now)
    commits = commit_sync(reads, s)
    return Compose(credit.lines + commits.lines, credit.events + commits.events)


def track_sync(journal_path: str, server: str, now: Instant) -> list[str]:
    """Tracker.Sync: credit the windows, then the commits, and return the lines to print.

    The external reads (ActivityWatch, git) happen first, with no lock: readers never take the
    lock, and a sync that has nothing to write takes none. The state is folded once without
    the lock to see whether anything is due. Only then does it take the lock, fold the state
    again under it, and write the facts in one batch with one fsync. The reads from the first
    pass are reused.
    """
    s0 = state_from(JournalTx(journal_path).read())
    if len(s0.all_watches()) == 0:
        return [NOTHING_WATCHED]
    reads = TrackReads()
    first = compose(reads, s0, server, now)
    if len(first.events) == 0:
        return first.lines

    tx = JournalTx(journal_path)
    tx.begin()
    try:
        s1 = state_from(tx.read())
        if len(s1.all_watches()) == 0:
            tx.abort()
            return [NOTHING_WATCHED]
        fresh = compose(reads, s1, server, now)
        for e in fresh.events:
            tx.append(e)
        tx.commit()
        return fresh.lines
    except BaseException:
        tx.abort()
        raise
