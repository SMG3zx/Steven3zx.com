"""The scripted cases for the tracker's differential test (sync_diff).

Each case says what ActivityWatch answers (windows, afk, and a failure mode per step), which
git repositories exist, and the commands to run with the clock pinned. Times are written in
the machine's zone (America/Chicago, -06:00 in November) or in UTC, as the text says. Each
binary gets its repositories built again with fixed dates, so both see the same commit
hashes at the same paths.
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path
from typing import Final

from .fake_activitywatch import Event
from .sync_model import Action, Scenario, Step

DAY1: Final = "2026-11-18"
DAY2: Final = "2026-11-19"
DAY3: Final = "2026-11-20"
AUTHOR: Final = {
    "GIT_AUTHOR_NAME": "Test Author",
    "GIT_AUTHOR_EMAIL": "author@example.invalid",
    "GIT_COMMITTER_NAME": "Test Committer",
    "GIT_COMMITTER_EMAIL": "committer@example.invalid",
}


def win(stamp: str, seconds: float, app: str | None, title: str | None) -> Event:
    """A window event: its start as AW writes it, its length, and the app and title."""
    return Event(stamp, seconds, {"app": app, "title": title})


def afk(stamp: str, seconds: float, status: str) -> Event:
    """An afk event: status is "not-afk" or "afk"."""
    return Event(stamp, seconds, {"status": status})


def local(day: str, clock: str) -> str:
    """A time in the machine's zone, as --now is written."""
    return f"{day}T{clock}-06:00"


def git(repo: Path, *args: str, when: str | None = None) -> None:
    """One git call in repo, with the author and committer fixed. when sets both dates."""
    env = {key: os.environ[key] for key in ("PATH", "HOME", "LANG", "LC_ALL") if key in os.environ}
    env.update(AUTHOR)
    if when is not None:
        env["GIT_AUTHOR_DATE"] = when
        env["GIT_COMMITTER_DATE"] = when
    subprocess.run(
        [
            "git",
            "-C",
            str(repo),
            "-c",
            "commit.gpgsign=false",
            "-c",
            "core.hooksPath=/dev/null",
            *args,
        ],
        env=env,
        check=True,
        capture_output=True,
    )


def commit(repo: Path, files: dict[str, str], subject: str, when: str) -> None:
    """Write the files, then commit them all with one subject at one time."""
    for rel, text in files.items():
        path = repo / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        git(repo, "add", rel)
    git(repo, "commit", "-q", "-m", subject, when=when)


def build_monorepo(root: Path) -> None:
    """Commits with fixed dates, under alpha, alpha/beta, gamma, gamma/old and one unrelated
    folder. One subject has a tab in it."""
    repo = root / "monorepo"
    repo.mkdir(parents=True)
    git(repo, "init", "-q", "-b", "main")
    commit(repo, {"alpha/a.txt": "one\n"}, "alpha one", "2026-11-16T10:00:00-06:00")
    commit(
        repo,
        {"alpha/beta/b.txt": "beta\n"},
        "beta one\twith tab",
        "2026-11-16T11:00:00-06:00",
    )
    commit(
        repo,
        {"alpha/a.txt": "one again\n", "alpha/beta/b.txt": "beta again\n"},
        "both",
        "2026-11-17T09:00:00-06:00",
    )
    commit(repo, {"gamma/x.txt": "gamma\n"}, "gamma", "2026-11-17T10:00:00-06:00")
    commit(repo, {"gamma/old/o.txt": "old\n"}, "old thing", "2026-11-17T11:00:00-06:00")
    commit(repo, {"other/u.txt": "u\n"}, "unrelated", "2026-11-17T12:00:00-06:00")


def later_commit(files: dict[str, str], subject: str, when: str) -> Action:
    """A repository change made before a step, the same on both binaries' runs."""

    def act(root: Path) -> None:
        commit(root / "monorepo", files, subject, when)

    return act


def ideas_and_watches(*pairs: str) -> tuple[Step, ...]:
    """One idea per pair, then its watch, all at 09:00 with tracking off.

    A pair is "title" then the watched text, separated by a unit separator (\\x1f), and an
    optional third part, "use", for a watch that counts as use.
    """
    now = local(DAY1, "09:00:00")
    out: list[Step] = []
    for n, pair in enumerate(pairs, start=1):
        parts = pair.split("\x1f")
        out.append(Step(argv=("idea", parts[0]), now=now, track=False))
        out.append(Step(argv=("watch", f"i{n}", *parts[1:]), now=now, track=False))
    return tuple(out)


def scenarios() -> list[Scenario]:
    """Every case, in the order the report prints them."""
    no_server = Scenario(
        name="no_server",
        description="ActivityWatch refuses the connection: no window time, commits still read",
        server="closed",
        setup=ideas_and_watches("Glider\x1fglider"),
        steps=(
            Step(argv=("sync",), now=local(DAY1, "11:00:00")),
            Step(argv=("sync",), now=local(DAY1, "11:00:00")),
            Step(argv=("watches",), now=local(DAY1, "11:00:00")),
            Step(argv=("spent", "i1", "5"), now=local(DAY1, "11:05:00")),
        ),
    )

    empty_buckets = Scenario(
        name="empty_buckets",
        description="The watcher has no windows; a gap longer than a day still writes sync",
        afk=(
            afk("2026-11-18T15:00:00Z", 14400, "not-afk"),
            afk("2026-11-19T15:00:00Z", 14400, "not-afk"),
        ),
        setup=ideas_and_watches("Glider\x1fglider"),
        steps=(
            Step(argv=("sync",), now=local(DAY1, "10:00:00")),
            Step(argv=("sync",), now=local(DAY1, "10:30:00")),
            Step(argv=("sync",), now=local(DAY2, "11:00:00")),
        ),
    )

    idle_and_titles = Scenario(
        name="idle_and_titles",
        description="afk gaps, a window cut by afk, nanosecond stamps, offsets, a null title",
        windows=(
            win("2026-11-18T15:10:00Z", 600, "code", "Glider.cs - glider - Code"),
            win("2026-11-18T15:30:00Z", 300, "firefox", "Docs"),
            win("2026-11-18T16:20:00Z", 900, "code", "glider notes"),
            win("2026-11-18T17:10:00.123456789Z", 120, "terminal", "glider run"),
            win("2026-11-18T09:20:00-06:00", 240, "code", "glider early"),
            win("2026-11-18T17:20:00+00:00", 30, "x", None),
        ),
        afk=(
            afk("2026-11-18T15:00:00Z", 5400, "not-afk"),
            afk("2026-11-18T16:30:00Z", 1800, "afk"),
            afk("2026-11-18T17:00:00Z", 3600, "not-afk"),
        ),
        setup=ideas_and_watches("Glider\x1fglider"),
        steps=(
            Step(argv=("sync",), now=local(DAY1, "12:00:00")),
            Step(argv=("sync",), now=local(DAY1, "12:00:00")),
            Step(argv=("show", "i1"), now=local(DAY1, "12:00:00")),
            Step(argv=("sync",), now=local(DAY1, "12:05:00")),
        ),
    )

    midnight_and_use = Scenario(
        name="midnight_and_use",
        description="Windows across local midnight; one used fact a local day; a sub-minute "
        "use; a use watch on an idea that never shipped",
        windows=(
            win("2026-11-19T05:55:00Z", 600, "code", "glider midnight"),
            win("2026-11-19T05:50:00Z", 1200, "browser", "dashboard late"),
            win("2026-11-19T15:00:00Z", 300, "browser", "dashboard morning"),
            win("2026-11-19T15:30:00Z", 30, "browser", "dashboard peek"),
            win("2026-11-19T15:45:00Z", 300, "x", "tool window"),
            win("2026-11-19T16:10:00Z", 120, "browser", "dashboard again"),
            win("2026-11-20T15:00:00Z", 120, "browser", "dashboard day three"),
            win("2026-11-20T17:30:00Z", 45, "browser", "dashboard blip"),
        ),
        afk=(afk("2026-11-18T15:00:00Z", 259200, "not-afk"),),
        journal=(
            '{"at":"2026-11-18T09:00:00-06:00","kind":"idea","id":"i1","text":"Glider"}',
            '{"at":"2026-11-18T09:05:00-06:00","kind":"stage","id":"i1","text":"Build"}',
            '{"at":"2026-11-18T09:00:00-06:00","kind":"idea","id":"i2","text":"Other"}',
            '{"at":"2026-11-18T09:05:00-06:00","kind":"stage","id":"i2","text":"Build"}',
        ),
        setup=(
            Step(argv=("watch", "i1", "glider"), now=local(DAY1, "09:00:00"), track=False),
            Step(argv=("watch", "i2", "tool", "use"), now=local(DAY1, "09:00:00"), track=False),
        ),
        steps=(
            Step(argv=("ship", "i1", "first version"), now=local(DAY1, "09:30:00"), track=False),
            Step(
                argv=("watch", "i1", "dashboard", "use"), now=local(DAY1, "09:31:00"), track=False
            ),
            Step(argv=("sync",), now=local(DAY2, "10:00:00")),
            Step(argv=("sync",), now=local(DAY2, "10:30:00")),
            Step(argv=("sync",), now=local(DAY3, "11:00:00")),
            Step(argv=("sync",), now=local(DAY3, "13:00:00")),
        ),
    )

    titles = Scenario(
        name="titles",
        description="The longest watch wins; the match folds non-ASCII letters as .NET does",
        windows=(
            win("2026-11-18T15:10:00Z", 600, "code", "Glider Wing.cs - glider wing - Code"),
            win("2026-11-18T15:30:00Z", 300, "code", "Glider.cs - glider - Code"),
            win("2026-11-18T15:40:00Z", 120, "browser", "PROBE ÜBER"),
            win("2026-11-18T15:50:00Z", 60, "browser", "STRASSE"),
            win("2026-11-18T16:00:00Z", 60, "browser", "straße"),
            win("2026-11-18T16:10:00Z", 90, "x", "Wing"),
        ),
        afk=(afk("2026-11-18T15:00:00Z", 14400, "not-afk"),),
        setup=ideas_and_watches(
            "Wing\x1fglider wing",
            "Glider\x1fglider",
            "Umlaut\x1füber",
            "Strasse\x1fstraße",
        ),
        steps=(Step(argv=("sync",), now=local(DAY1, "11:00:00")),),
    )

    git_commits = Scenario(
        name="git_commits",
        description="Commits under homes: latest on the first sync, then since; deepest home "
        "wins; a retired home is skipped; a tab and a non-ASCII letter in subjects",
        repos=build_monorepo,
        windows=(win("2026-11-18T15:10:00Z", 600, "code", "b.txt - alpha - Code"),),
        afk=(afk("2026-11-18T15:00:00Z", 7200, "not-afk"),),
        setup=(
            Step(argv=("idea", "Alpha"), now=local(DAY1, "09:00:00"), track=False),
            Step(
                argv=("home", "i1", "{repos}/monorepo/alpha"),
                now=local(DAY1, "09:00:00"),
                track=False,
            ),
            Step(argv=("idea", "Beta"), now=local(DAY1, "09:00:01"), track=False),
            Step(
                argv=("home", "i2", "{repos}/monorepo/alpha/beta"),
                now=local(DAY1, "09:00:01"),
                track=False,
            ),
            Step(argv=("idea", "Gamma"), now=local(DAY1, "09:00:02"), track=False),
            Step(
                argv=("home", "i3", "{repos}/monorepo/gamma"),
                now=local(DAY1, "09:00:02"),
                track=False,
            ),
            Step(argv=("idea", "Old"), now=local(DAY1, "09:00:03"), track=False),
            Step(
                argv=("home", "i4", "{repos}/monorepo/gamma/old"),
                now=local(DAY1, "09:00:03"),
                track=False,
            ),
            Step(argv=("retire", "i4", "moved"), now=local(DAY1, "09:00:04"), track=False),
        ),
        steps=(
            Step(argv=("sync",), now=local(DAY1, "09:30:00")),
            Step(
                argv=("sync",),
                now=local(DAY1, "09:45:00"),
                actions=(
                    later_commit(
                        {"alpha/beta/b.txt": "beta two\n"}, "beta two", "2026-11-18T08:00:00-06:00"
                    ),
                ),
            ),
            Step(
                argv=("sync",),
                now=local(DAY1, "10:00:00"),
                actions=(
                    later_commit(
                        {"alpha/a.txt": "alpha two\n"}, "alpha two", "2026-11-18T09:00:00-06:00"
                    ),
                    later_commit(
                        {"gamma/old/o2.txt": "old two\n"}, "old two", "2026-11-18T09:10:00-06:00"
                    ),
                ),
            ),
            Step(argv=("sync",), now=local(DAY1, "10:00:00")),
            Step(
                argv=("sync",),
                now=local(DAY1, "11:30:00"),
                actions=(
                    later_commit(
                        {"gamma/x.txt": "gamma again\n"}, "gamma ü", "2026-11-18T11:00:00-06:00"
                    ),
                ),
            ),
        ),
    )

    errors_then_recover = Scenario(
        name="errors_then_recover",
        description="Each failed read writes nothing; the next good read covers the same period",
        windows=(win("2026-11-18T15:10:00Z", 600, "code", "glider"),),
        afk=(afk("2026-11-18T15:00:00Z", 7200, "not-afk"),),
        setup=ideas_and_watches("Glider\x1fglider"),
        steps=(
            Step(argv=("sync",), now=local(DAY1, "10:00:00"), mode="http500"),
            Step(argv=("sync",), now=local(DAY1, "10:05:00"), mode="notarray"),
            Step(argv=("sync",), now=local(DAY1, "10:10:00"), mode="badjson"),
            Step(argv=("sync",), now=local(DAY1, "10:15:00"), mode="noafk"),
            Step(argv=("sync",), now=local(DAY1, "11:00:00"), mode="ok"),
        ),
    )

    rounding = Scenario(
        name="rounding",
        description="Minutes round to two places, to even on a half; one tick credits nothing",
        windows=(
            win("2026-11-18T15:10:00Z", 7.5, "a", "glider"),
            win("2026-11-18T15:20:00Z", 22.5, "a", "wing"),
            win("2026-11-18T15:30:00Z", 0.5, "a", "tiny"),
            win("2026-11-18T15:40:00Z", 1e-7, "a", "micro"),
        ),
        afk=(afk("2026-11-18T15:00:00Z", 7200, "not-afk"),),
        setup=ideas_and_watches("A\x1fglider", "B\x1fwing", "C\x1ftiny", "D\x1fmicro"),
        steps=(Step(argv=("sync",), now=local(DAY1, "11:00:00")),),
    )

    no_watches = Scenario(
        name="no_watches",
        description="Nothing watched: sync says so and reads nothing from ActivityWatch",
        setup=(Step(argv=("idea", "Idle"), now=local(DAY1, "09:00:00"), track=False),),
        steps=(
            Step(argv=("sync",), now=local(DAY1, "10:00:00")),
            Step(argv=("watches",), now=local(DAY1, "10:00:00")),
        ),
    )

    no_track = Scenario(
        name="no_track",
        description="VINCI_NO_TRACK=1 reads nothing and writes nothing for the sync",
        windows=(win("2026-11-18T15:10:00Z", 600, "code", "glider"),),
        afk=(afk("2026-11-18T15:00:00Z", 7200, "not-afk"),),
        setup=ideas_and_watches("Glider\x1fglider"),
        steps=(
            Step(argv=("sync",), now=local(DAY1, "11:00:00"), track=False),
            Step(argv=("sync",), now=local(DAY1, "11:00:00")),
        ),
    )

    return [
        no_server,
        empty_buckets,
        idle_and_titles,
        midnight_and_use,
        titles,
        git_commits,
        errors_then_recover,
        rounding,
        no_watches,
        no_track,
    ]
