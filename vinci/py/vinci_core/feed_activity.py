"""The activity feed: the window time ActivityWatch recorded, one record per window title per
local day. Source "activity", citation prefix "w", its own search table (fts_activity), so no
other source's scores move when it is loaded.

A run reads each window bucket from its cursor, the start of the last window event seen. Each
local day the new events touch is then read again in full, across every bucket, and all its
records are rebuilt. A record whose totals did not change keeps its hash, so a second run with no
new events writes nothing. The read is never complete, so a record is never retracted here:
nothing says a window was removed from the watcher's history.

Windows on the built-in deny list (and any the config adds) are dropped before they are counted.
The time away from the keyboard is cut by the server (afk), as the tracker's query does.

Port of mojo/src/feed_activity.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass

from vinci_edge import feed_edge

from .feed_store import ACTIVITY_SOURCE, FeedRead, FeedRecord, feed_record
from .index_store import sha256_hex
from .text import day as day_text, span
from .timeutil import (
    DAYS_0001_TO_1970,
    TICKS_PER_DAY,
    TICKS_PER_HOUR,
    TICKS_PER_MINUTE,
    Instant,
    format_instant,
    parse_instant,
    zero_pad,
)
from .track import local_at, track_fold, utc_o

# Stretches of one window separated by at most this much are one range in the text.
RANGE_GAP = 60 * 1000 * 10000  # 60 s in ticks
# The ranges written into a record's text. The rest are counted, not listed.
RANGES_LISTED = 20
WINDOW_KIND = "window"
KEY_SEPARATOR = "\x1f"


@dataclass
class Piece:
    """One stretch a window had the focus, with both ends in the local offset."""

    start: Instant
    end: Instant
    app: str
    title: str


@dataclass
class Group:
    """The pieces of one window on one local day, before they are summed."""

    day: str
    app: str
    title: str
    pieces: list[Piece]


@dataclass
class Bucket:
    """A window bucket's cursor: the id, and the start text of the last event read."""

    id: str
    at: str


def one_line(text: str) -> str:
    """The text with tabs and line breaks turned into spaces, so the title is one line."""
    return text.replace("\t", " ").replace("\n", " ").replace("\r", " ")


def window_title(app: str, title: str) -> str:
    """The record's title: the app, then the window title after it."""
    a = one_line(app)
    t = one_line(title)
    if t == "":
        return a
    if a == "":
        return t
    return a + ": " + t


def hour_minute(at: Instant) -> str:
    """HH:MM of a local instant."""
    rest = at.local_ticks() % TICKS_PER_DAY
    hour = rest // TICKS_PER_HOUR
    minute = (rest // TICKS_PER_MINUTE) % 60
    return zero_pad(hour, 2) + ":" + zero_pad(minute, 2)


def local_day_number(at: Instant) -> int:
    """The local calendar day as days since 1970-01-01."""
    return at.local_ticks() // TICKS_PER_DAY - DAYS_0001_TO_1970


def range_text(pieces: list[Piece]) -> str:
    """The merged stretches as HH:MM-HH:MM, at most RANGES_LISTED of them.

    Pieces are sorted first. A piece that starts within RANGE_GAP of the last range's end
    extends that range; otherwise it begins a new one.
    """
    starts: list[Instant] = []
    ends: list[Instant] = []
    for p in pieces:
        if len(starts) > 0 and p.start.utc - ends[-1].utc <= RANGE_GAP:
            if p.end.utc > ends[-1].utc:
                ends[-1] = p.end
        else:
            starts.append(p.start)
            ends.append(p.end)
    parts = [hour_minute(starts[i]) + "-" + hour_minute(ends[i]) for i in range(min(len(starts), RANGES_LISTED))]
    text = ", ".join(parts)
    if len(starts) > RANGES_LISTED:
        text += " (+" + str(len(starts) - RANGES_LISTED) + " more)"
    return text


def record_of(group: Group) -> FeedRecord:
    """One window on one day as a record: its id from the day and the window's key, its time as
    the first stretch, and its text: the title, then the day's total and ranges."""
    pieces = sorted(group.pieces, key=lambda p: p.start.utc)
    total = 0
    for p in pieces:
        total += p.end.utc - p.start.utc
    title = window_title(group.app, group.title)
    digest = sha256_hex(group.app + KEY_SEPARATOR + group.title)
    ident = group.day + "/" + digest[:16]
    body = group.day + ": " + span(total) + " in this window, " + range_text(pieces)
    return feed_record(
        ACTIVITY_SOURCE,
        ident,
        format_instant(pieces[0].start),
        WINDOW_KIND,
        title,
        "",
        title + "\n" + body,
        [],
    )


def aggregate(pieces: list[Piece]) -> list[FeedRecord]:
    """The records for pieces that each lie within one local day: one per window per day.

    A piece's day is the local day of its start. Pieces of the same window and day are one
    record, whichever bucket they came from.
    """
    groups: list[Group] = []
    at: dict[str, int] = {}
    for p in pieces:
        day_str = day_text(p.start)
        key = day_str + KEY_SEPARATOR + p.app + KEY_SEPARATOR + p.title
        if key not in at:
            groups.append(Group(day_str, p.app, p.title, []))
            at[key] = len(groups) - 1
        groups[at[key]].pieces.append(p)
    return [record_of(g) for g in groups]


def denied(folded_text: str, folded_deny: list[str]) -> bool:
    """True when a folded deny term is inside the folded "app title" text."""
    return any(term in folded_text for term in folded_deny)


def read_cursor(text: str) -> list[Bucket]:
    """The cursor's entries, one "bucket=start" per bucket, separated by semicolons."""
    out: list[Bucket] = []
    if text == "":
        return out
    for entry in text.split(";"):
        pair = entry.split("=")
        if len(pair) != 2:
            raise ValueError("activity: the cursor is not bucket=start pairs")
        out.append(Bucket(pair[0], pair[1]))
    return out


def cursor_at(cursor: list[Bucket], ident: str) -> str:
    """The stored start of a bucket, or "" when it has none yet."""
    for b in cursor:
        if b.id == ident:
            return b.at
    return ""


def epoch() -> Instant:
    """1970-01-01T00:00:00 at offset zero: where a bucket with no cursor starts."""
    return Instant(DAYS_0001_TO_1970 * TICKS_PER_DAY, 0)


def local_midnight(day_number: int) -> Instant:
    """The local midnight that starts a local day, given as days since 1970-01-01.

    The offset is asked at the midnight as UTC, then again at the instant that gives, so a day
    whose midnight is across a daylight change still starts at the right moment.
    """
    wall = (day_number + DAYS_0001_TO_1970) * TICKS_PER_DAY
    first = local_at(Instant(wall, 0)).offset
    instant = wall - first * TICKS_PER_MINUTE
    offset = local_at(Instant(instant, 0)).offset
    return Instant(wall - offset * TICKS_PER_MINUTE, offset)


def pieces_of(server: str, window_id: str, afk_id: str, from_at: Instant, to_at: Instant, folded_deny: list[str]) -> list[Piece]:
    """One bucket pair's windows in a period, the denied ones left out, with local ends.
    folded_deny is the deny list folded the way the window text is (track_fold)."""
    status, rows = feed_edge.activity_pieces(server, window_id, afk_id, utc_o(from_at), utc_o(to_at))
    if status != "ok":
        raise ValueError("activity: " + str(rows))
    texts = [str(row[2]) + " " + str(row[3]) for row in rows]
    folded = track_fold(texts)
    out: list[Piece] = []
    for i, text in enumerate(texts):
        if denied(folded[i], folded_deny):
            continue
        row = rows[i]
        ticks = int(row[1])
        # A heartbeat of no length says nothing about time on screen.
        if ticks <= 0:
            continue
        start = parse_instant(str(row[0]))
        end = start.add_ticks(ticks)
        out.append(Piece(local_at(start), local_at(end), str(row[2]), str(row[3])))
    return out


def read_activity(server: str, deny: list[str], cursor: str, now: Instant) -> FeedRead:
    """Read the window buckets since the cursor and rebuild the days they touch.

    Raises when the server cannot be read, so the run writes nothing and the others still run.
    """
    status, listing = feed_edge.activity_buckets(server)
    if status != "ok":
        raise ValueError("activity: " + str(listing))
    pairs = listing["pairs"]
    skipped = listing["skipped"]
    old = read_cursor(cursor)
    folded_deny = track_fold(deny)
    touched: list[int] = []
    next_entries: list[str] = []
    ids: list[str] = []
    seen_events = 0

    for pair in pairs:
        window_id = str(pair[0])
        afk_id = str(pair[1])
        from_at = epoch()
        prior = cursor_at(old, window_id)
        if prior != "":
            from_at = parse_instant(prior)
        reply_status, rows = feed_edge.activity_pieces(server, window_id, afk_id, utc_o(from_at), utc_o(now))
        if reply_status != "ok":
            raise ValueError("activity: " + str(rows))
        newest = prior
        texts = [str(row[2]) + " " + str(row[3]) for row in rows]
        folded = track_fold(texts)
        for i, row in enumerate(rows):
            seen_events += 1
            start = parse_instant(str(row[0]))
            stamp = format_instant(start)
            if newest == "" or start.utc > parse_instant(newest).utc:
                newest = stamp
            ticks = int(row[1])
            if ticks <= 0 or denied(folded[i], folded_deny):
                continue
            end = start.add_ticks(ticks)
            touched.append(local_day_number(local_at(start)))
            touched.append(local_day_number(local_at(end)))
        if newest != "":
            next_entries.append(window_id + "=" + newest)
        ids.append(window_id)

    # The days touched, each once, in order: every one is read again in full.
    days: list[int] = []
    for d in touched:
        if d not in days:
            days.append(d)
    records: list[FeedRecord] = []
    windows_seen = 0
    for d in days:
        start = local_midnight(d)
        stop = local_midnight(d + 1)
        stop_read = stop if stop.utc < now.utc else now
        day_pieces: list[Piece] = []
        for pair in pairs:
            day_pieces.extend(pieces_of(server, str(pair[0]), str(pair[1]), start, stop_read, folded_deny))
        made = aggregate(day_pieces)
        windows_seen += len(made)
        records.extend(made)

    joined = ";".join(next_entries)
    # A bucket that is no longer listed keeps its cursor, so a later return picks up from it.
    for b in old:
        if b.id not in ids:
            if joined != "":
                joined += ";"
            joined += b.id + "=" + b.at
    note = str(len(pairs)) + " window bucket" + ("" if len(pairs) == 1 else "s") + ", " + str(seen_events) + " events read"
    if len(skipped) > 0:
        note += ", " + str(len(skipped)) + " without an afk bucket skipped"
    return FeedRead(records, [], False, joined, windows_seen, note)
