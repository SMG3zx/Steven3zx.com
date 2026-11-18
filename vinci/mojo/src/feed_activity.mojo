# The activity feed (M5 step b, docs/feeds.md): the window time ActivityWatch recorded, one record
# per window title per local day. Source "activity", citation prefix "w", its own search table
# (fts_activity), so no other source's scores move when it is loaded.
#
# A run reads each window bucket from its cursor, the start of the last window event seen. Each
# local day the new events touch is then read again in full, across every bucket, and all its
# records are rebuilt. A record whose totals did not change keeps its hash, so a second run with
# no new events writes nothing. The read is never complete, so a record is never retracted here:
# nothing says a window was removed from the watcher's history.
#
# Windows on the built-in deny list (and any the config adds) are dropped before they are
# counted. The time away from the keyboard is cut by the server (afk), as the tracker's query does.

from feed_edge import activity_buckets, activity_pieces
from feed_store import ACTIVITY_SOURCE, FeedRead, FeedRecord, feed_record
from index_store import sha256_hex
from text import day, span
from timeutil import (
    DAYS_0001_TO_1970,
    TICKS_PER_DAY,
    TICKS_PER_HOUR,
    TICKS_PER_MINUTE,
    Instant,
    ascii_prefix,
    format_instant,
    parse_instant,
    zero_pad,
)
from track import local_at, track_fold, utc_o

# Stretches of one window separated by at most this much are one range in the text.
comptime RANGE_GAP = 60 * 1000 * 10000  # 60 s in ticks
# The ranges written into a record's text. The rest are counted, not listed.
comptime RANGES_LISTED = 20
comptime WINDOW_KIND = "window"
comptime KEY_SEPARATOR = "\x1f"


@fieldwise_init
struct Piece(Copyable, Movable):
    """One stretch a window had the focus, with both ends in the local offset.
    """

    var start: Instant
    var end: Instant
    var app: String
    var title: String


@fieldwise_init
struct Group(Copyable, Movable):
    """The pieces of one window on one local day, before they are summed."""

    var day: String
    var app: String
    var title: String
    var pieces: List[Piece]


@fieldwise_init
struct Bucket(Copyable, Movable):
    """A window bucket's cursor: the id, and the start text of the last event read.
    """

    var id: String
    var at: String


def one_line(text: String) raises -> String:
    """The text with tabs and line breaks turned into spaces, so the title is one line.
    """
    var out = List[UInt8]()
    for byte in text.as_bytes():
        if byte == 9 or byte == 10 or byte == 13:
            out.append(32)
        else:
            out.append(byte)
    return String(unsafe_from_utf8=out)


def window_title(app: String, title: String) raises -> String:
    """The record's title: the app, then the window title after it."""
    var a = one_line(app)
    var t = one_line(title)
    if t.byte_length() == 0:
        return a
    if a.byte_length() == 0:
        return t
    return a + ": " + t


def hour_minute(at: Instant) -> String:
    """HH:MM of a local instant."""
    var rest = at.local_ticks() % TICKS_PER_DAY
    var hour = rest // TICKS_PER_HOUR
    var minute = (rest // TICKS_PER_MINUTE) % 60
    return zero_pad(hour, 2) + ":" + zero_pad(minute, 2)


def local_day_number(at: Instant) -> Int:
    """The local calendar day as days since 1970-01-01."""
    return at.local_ticks() // TICKS_PER_DAY - DAYS_0001_TO_1970


def insertion_sorted(mut pieces: List[Piece]):
    """The pieces by start time, oldest first. A stable insertion sort: the lists are short.
    """
    for i in range(1, len(pieces)):
        var j = i
        while j > 0 and pieces[j - 1].start.utc > pieces[j].start.utc:
            var swap = pieces[j - 1].copy()
            pieces[j - 1] = pieces[j].copy()
            pieces[j] = swap^
            j -= 1


def range_text(pieces: List[Piece]) raises -> String:
    """The merged stretches as HH:MM-HH:MM, at most RANGES_LISTED of them.

    Pieces are sorted first. A piece that starts within RANGE_GAP of the last range's end
    extends that range; otherwise it begins a new one.
    """
    var starts = List[Instant]()
    var ends = List[Instant]()
    for p in pieces:
        if (
            len(starts) > 0
            and p.start.utc - ends[len(ends) - 1].utc <= RANGE_GAP
        ):
            if p.end.utc > ends[len(ends) - 1].utc:
                ends[len(ends) - 1] = p.end.copy()
        else:
            starts.append(p.start.copy())
            ends.append(p.end.copy())
    var parts = List[String]()
    for i in range(len(starts)):
        if i == RANGES_LISTED:
            break
        parts.append(hour_minute(starts[i]) + "-" + hour_minute(ends[i]))
    var text = String()
    for i in range(len(parts)):
        if i > 0:
            text += ", "
        text += parts[i]
    if len(starts) > RANGES_LISTED:
        text += " (+" + String(len(starts) - RANGES_LISTED) + " more)"
    return text


def record_of(group: Group) raises -> FeedRecord:
    """One window on one day as a record: its id from the day and the window's key, its
    time as the first stretch, and its text: the title, then the day's total and ranges.
    """
    var pieces = group.pieces.copy()
    insertion_sorted(pieces)
    var total = 0
    for p in pieces:
        total += p.end.utc - p.start.utc
    var title = window_title(group.app, group.title)
    var digest = sha256_hex(group.app + KEY_SEPARATOR + group.title)
    var id = group.day + "/" + ascii_prefix(digest, 16)
    var body = (
        group.day
        + ": "
        + span(total)
        + " in this window, "
        + range_text(pieces)
    )
    return feed_record(
        ACTIVITY_SOURCE,
        id,
        format_instant(pieces[0].start),
        WINDOW_KIND,
        title,
        "",
        title + "\n" + body,
        List[String](),
    )


def group_index(
    mut groups: List[Group],
    mut at: Dict[String, Int],
    key: String,
    day_text: String,
    app: String,
    title: String,
) raises -> Int:
    """The group for a key, made when it is new."""
    if key in at:
        return at[key]
    groups.append(Group(day_text, app, title, List[Piece]()))
    var index = len(groups) - 1
    at[key] = index
    return index


def aggregate(pieces: List[Piece]) raises -> List[FeedRecord]:
    """The records for pieces that each lie within one local day: one per window per day.

    A piece's day is the local day of its start. Pieces of the same window and day are one
    record, whichever bucket they came from.
    """
    var groups = List[Group]()
    var at = Dict[String, Int]()
    for p in pieces:
        var day_text = day(p.start)
        var key = day_text + KEY_SEPARATOR + p.app + KEY_SEPARATOR + p.title
        var index = group_index(groups, at, key, day_text, p.app, p.title)
        groups[index].pieces.append(p.copy())
    var out = List[FeedRecord]()
    for g in groups:
        out.append(record_of(g))
    return out^


def denied(folded_text: String, folded_deny: List[String]) -> Bool:
    """True when a folded deny term is inside the folded "app title" text."""
    for term in folded_deny:
        if term in folded_text:
            return True
    return False


def read_cursor(text: String) raises -> List[Bucket]:
    """The cursor's entries, one "bucket=start" per bucket, separated by semicolons.
    """
    var out = List[Bucket]()
    if text.byte_length() == 0:
        return out^
    for entry in text.split(";"):
        var pair = String(entry).split("=")
        if len(pair) != 2:
            raise Error("activity: the cursor is not bucket=start pairs")
        out.append(Bucket(String(pair[0]), String(pair[1])))
    return out^


def cursor_at(cursor: List[Bucket], id: String) -> String:
    """The stored start of a bucket, or "" when it has none yet."""
    for b in cursor:
        if b.id == id:
            return b.at
    return ""


def epoch() raises -> Instant:
    """1970-01-01T00:00:00 at offset zero: where a bucket with no cursor starts.
    """
    return Instant(DAYS_0001_TO_1970 * TICKS_PER_DAY, 0)


def local_midnight(day_number: Int) raises -> Instant:
    """The local midnight that starts a local day, given as days since 1970-01-01.

    The offset is asked at the midnight as UTC, then again at the instant that gives, so a day
    whose midnight is across a daylight change still starts at the right moment.
    """
    var wall = (day_number + DAYS_0001_TO_1970) * TICKS_PER_DAY
    var first = local_at(Instant(wall, 0)).offset
    var instant = wall - first * TICKS_PER_MINUTE
    var offset = local_at(Instant(instant, 0)).offset
    return Instant(wall - offset * TICKS_PER_MINUTE, offset)


def pieces_of(
    server: String,
    window_id: String,
    afk_id: String,
    from_at: Instant,
    to_at: Instant,
    folded_deny: List[String],
) raises -> List[Piece]:
    """One bucket pair's windows in a period, the denied ones left out, with local ends.
    folded_deny is the deny list folded the way the window text is (track_fold).
    """
    var reply = activity_pieces(
        server, window_id, afk_id, utc_o(from_at), utc_o(to_at)
    )
    if String(reply.status) != "ok":
        raise Error("activity: " + String(reply.value))
    var rows = reply.value.copy()
    var texts = List[String]()
    for row in rows:
        texts.append(String(row[2]) + " " + String(row[3]))
    var folded = track_fold(texts)
    var out = List[Piece]()
    for i in range(len(texts)):
        if denied(folded[i], folded_deny):
            continue
        var row = rows[i]
        var ticks = Int(String(row[1]))
        # A heartbeat of no length says nothing about time on screen.
        if ticks <= 0:
            continue
        var start = parse_instant(String(row[0]))
        var end = start.add_ticks(ticks)
        out.append(
            Piece(
                local_at(start),
                local_at(end),
                String(row[2]),
                String(row[3]),
            )
        )
    return out^


def read_activity(
    server: String,
    deny: List[String],
    cursor: String,
    now: Instant,
) raises -> FeedRead:
    """Read the window buckets since the cursor and rebuild the days they touch.

    Raises when the server cannot be read, so the run writes nothing and the others still run.
    """
    var listing = activity_buckets(server)
    if String(listing.status) != "ok":
        raise Error("activity: " + String(listing.value))
    var pairs = listing.value["pairs"]
    var skipped = listing.value["skipped"]
    var old = read_cursor(cursor)
    var folded_deny = track_fold(deny)
    var touched = List[Int]()
    var next_entries = List[String]()
    var ids = List[String]()
    var seen_events = 0

    for pair in pairs:
        var window_id = String(pair[0])
        var afk_id = String(pair[1])
        var from_at = epoch()
        var prior = cursor_at(old, window_id)
        if prior.byte_length() > 0:
            from_at = parse_instant(prior)
        var reply = activity_pieces(
            server, window_id, afk_id, utc_o(from_at), utc_o(now)
        )
        if String(reply.status) != "ok":
            raise Error("activity: " + String(reply.value))
        var rows = reply.value.copy()
        var newest = String(prior)
        var texts = List[String]()
        for row in rows:
            texts.append(String(row[2]) + " " + String(row[3]))
        var folded = track_fold(texts)
        for i in range(len(rows)):
            seen_events += 1
            var start = parse_instant(String(rows[i][0]))
            var stamp = format_instant(start)
            if (
                newest.byte_length() == 0
                or start.utc > parse_instant(newest).utc
            ):
                newest = stamp
            var ticks = Int(String(rows[i][1]))
            if ticks <= 0 or denied(folded[i], folded_deny):
                continue
            var end = start.add_ticks(ticks)
            touched.append(local_day_number(local_at(start)))
            touched.append(local_day_number(local_at(end)))
        if newest.byte_length() > 0:
            next_entries.append(window_id + "=" + newest)
        ids.append(window_id)

    # The days touched, each once, in order: every one is read again in full.
    var days = List[Int]()
    for d in touched:
        var known = False
        for e in days:
            if e == d:
                known = True
        if not known:
            days.append(d)
    var records = List[FeedRecord]()
    var windows_seen = 0
    for d in days:
        var start = local_midnight(d)
        var stop = local_midnight(d + 1)
        var stop_read = stop.copy() if stop.utc < now.utc else now.copy()
        var day_pieces = List[Piece]()
        for pair in pairs:
            for p in pieces_of(
                server,
                String(pair[0]),
                String(pair[1]),
                start,
                stop_read,
                folded_deny,
            ):
                day_pieces.append(p.copy())
        var made = aggregate(day_pieces)
        windows_seen += len(made)
        for rec in made:
            records.append(rec.copy())

    var joined = String()
    for i in range(len(next_entries)):
        if i > 0:
            joined += ";"
        joined += next_entries[i]
    # A bucket that is no longer listed keeps its cursor, so a later return picks up from it.
    for b in old:
        if b.id not in ids:
            if joined.byte_length() > 0:
                joined += ";"
            joined += b.id + "=" + b.at
    var note = (
        String(len(pairs))
        + " window bucket"
        + ("" if len(pairs) == 1 else "s")
        + ", "
        + String(seen_events)
        + " events read"
    )
    if len(skipped) > 0:
        note += ", " + String(len(skipped)) + " without an afk bucket skipped"
    return FeedRead(
        records^,
        List[String](),
        False,
        joined^,
        windows_seen,
        note^,
    )
