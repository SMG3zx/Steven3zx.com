"""The tracker's reads from outside vinci: the ActivityWatch query, git log, the local offset
of an instant, and the case folding that OrdinalIgnoreCase uses. Mojo does the arithmetic,
the credit and the journal lines (src/track.mojo), as src/Tracker.cs does.

Each function returns a status word first, so Mojo never parses a Python exception text:
("ok", value) or ("error", message). A failed read is "error" and Mojo treats it as the C#
does (null): no window time is added, and nothing is written for it.

The ActivityWatch values are returned as the server wrote them, except two conversions that
.NET makes when it reads them: a duration becomes whole ticks (TimeSpan.FromSeconds cuts the
product toward zero), and a fraction longer than seven digits is cut to seven (DateTimeOffset
keeps 100 ns ticks). Both are probed against the C# binary in docs/time-tracking.md.
"""

from __future__ import annotations

import json
import math
import os
import re
import subprocess
import time
import urllib.error
import urllib.request
from typing import Final, cast

# The query Tracker.cs sends (ActivityWatch.Query), line for line.
QUERY: Final = (
    'window = flood(query_bucket(find_bucket("aw-watcher-window_")));',
    'afk = flood(query_bucket(find_bucket("aw-watcher-afk_")));',
    'present = filter_keyvals(afk, "status", ["not-afk"]);',
    'RETURN = {"window": filter_period_intersect(window, present), "afk": afk};',
)
HTTP_TIMEOUT_S: Final = 10.0
TICKS_PER_SECOND: Final = 10_000_000
TIMESTAMP: Final = re.compile(r"^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})\.(\d+)(.*)$")
FRACTION_DIGITS: Final = 7


def member(obj: object, key: str) -> object:
    """obj[key] for a JSON object; a missing key or a non-object is an error."""
    if not isinstance(obj, dict):
        raise TypeError("not an object")
    fields = cast("dict[str, object]", obj)
    if key not in fields:
        raise KeyError(key)
    return fields[key]


def array_of(value: object) -> list[object]:
    if not isinstance(value, list):
        raise TypeError("not an array")
    return cast("list[object]", value)


def text_of(value: object) -> str:
    if not isinstance(value, str):
        raise TypeError("not a string")
    return value


def nullable_text(value: object) -> str:
    """GetString(): null reads as the empty string, which is how C# interpolates it."""
    if value is None:
        return ""
    return text_of(value)


def ticks_of(value: object) -> int:
    """GetDouble() then TimeSpan.FromSeconds: whole ticks, cut toward zero."""
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise TypeError("not a number")
    seconds = float(value)
    if not math.isfinite(seconds):
        raise ValueError("not finite")
    return int(seconds * TICKS_PER_SECOND)


def timestamp_of(value: object) -> str:
    """The timestamp as .NET keeps it: at most seven fraction digits, cut, not rounded."""
    text = text_of(value)
    match = TIMESTAMP.match(text)
    if match is None:
        return text
    digits = match.group(2)
    return match.group(1) + "." + digits[:FRACTION_DIGITS] + match.group(3)


def activity(server: str, from_text: str, to_text: str) -> tuple[str, object]:
    """ActivityWatch.At: POST the query for one period. value is [windows, afk] when ok.

    windows rows are [timestamp, ticks, "app title"]; afk rows are [timestamp, ticks].
    Any failure is an error, as every failure is null in the C# (a non-2xx reply, a
    connection error, a body that is not the expected JSON).
    """
    body = json.dumps({"timeperiods": [f"{from_text}/{to_text}"], "query": list(QUERY)}).encode(
        "utf-8"
    )
    request = urllib.request.Request(
        server.rstrip("/") + "/api/0/query/",
        data=body,
        method="POST",
        headers={"Content-Type": "application/json; charset=utf-8"},
    )
    try:
        with urllib.request.urlopen(request, timeout=HTTP_TIMEOUT_S) as reply:
            if not 200 <= reply.status <= 299:
                return ("error", f"status {reply.status}")
            raw = reply.read()
    except (urllib.error.URLError, OSError, ValueError) as err:
        return ("error", str(err))
    try:
        doc = cast("object", json.loads(raw))
        if not isinstance(doc, list) or len(cast("list[object]", doc)) == 0:
            raise TypeError("the reply is not a non-empty array")
        result = cast("list[object]", doc)[0]
        windows: list[object] = []
        for event in array_of(member(result, "window")):
            data = member(event, "data")
            app = nullable_text(member(data, "app"))
            title = nullable_text(member(data, "title"))
            windows.append(
                [
                    timestamp_of(member(event, "timestamp")),
                    ticks_of(member(event, "duration")),
                    app + " " + title,
                ]
            )
        afk: list[object] = []
        for event in array_of(member(result, "afk")):
            afk.append(
                [
                    timestamp_of(member(event, "timestamp")),
                    ticks_of(member(event, "duration")),
                ]
            )
    except (TypeError, KeyError, ValueError) as err:
        return ("error", str(err))
    return ("ok", [windows, afk])


def fold_upper(texts: list[str]) -> tuple[str, list[str]]:
    """Each character's simple uppercase, as ToUpperInvariant gives it.

    A character whose uppercase is more than one character (the German sharp s) keeps its
    own form. Two strings are equal under OrdinalIgnoreCase when their folds are equal.
    """
    folded: list[str] = []
    for text in texts:
        chars: list[str] = []
        for ch in text:
            upper = ch.upper()
            chars.append(upper if len(upper) == 1 else ch)
        folded.append("".join(chars))
    return ("ok", folded)


def git_log(folder: str, after: int | None) -> tuple[str, str]:
    """Tracker.Log's git call, run as C# runs it. Output is "" when the folder is not a
    directory or git fails: C#'s Run returns "" then, so there is simply nothing to read.
    """
    if not os.path.isdir(folder):
        return ("ok", "")
    selector = f"--after={after}" if after is not None else "-1"
    args = [
        "git",
        "-C",
        folder,
        "log",
        "--format=%x01%H%x09%cI%x09%s",
        "--name-only",
        "--relative",
        "--reverse",
        selector,
        "--",
        ".",
    ]
    try:
        done = subprocess.run(
            args,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            check=False,
        )
    except OSError:
        return ("ok", "")
    if done.returncode != 0:
        return ("ok", "")
    return ("ok", done.stdout.decode("utf-8", errors="replace"))


def local_offset(unix_seconds: int) -> tuple[str, object]:
    """The machine's UTC offset in minutes east, at that instant (ToLocalTime's offset)."""
    try:
        seconds = time.localtime(unix_seconds).tm_gmtoff
    except (OverflowError, OSError, ValueError) as err:
        return ("error", str(err))
    return ("ok", int(seconds) // 60)
