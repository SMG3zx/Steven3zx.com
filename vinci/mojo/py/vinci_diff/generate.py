"""Seeded random command sequences built from the grammar in src/App.cs.

The generator reads the oracle's journal after every step, so the ids it uses are the ids
that exist. Most commands are valid; some are deliberately wrong (unknown ids, missing or
bad numbers, unknown commands) so the error paths get compared too. Numbers are bounded
so the oracle does not hit its TimeSpan and DateTime overflows (see oracle-bugs/).
"""

import json
import random
from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone

from .scenario import Step, as_object

Builder = Callable[["View"], list[str]]

WORDS = (
    "flight",
    "wing",
    "canal",
    "lock",
    "cloth",
    "test",
    "bird",
    "load",
    "pilot",
    "gear",
    "spar",
    "rope",
    "glider",
    "hill",
    "wind",
    "bench",
    "tune",
    "draft",
    "plan",
    "check",
)
METRICS = ("distance", "load", "defects", "minutes")
FOLDERS = ("glider", "canal", "nope")
PATTERNS = ("Code", "Terminal", "glider", "Firefox", "editor")
OFFSET_HOURS = (0, 0, 0, -5, 9)
START = datetime(2026, 11, 16, 9, 0)


@dataclass
class View:
    """What the journal says exists right now. Built by folding the oracle's events."""

    ideas: list[str] = field(default_factory=list[str])
    stage: dict[str, str] = field(default_factory=dict[str, str])
    reqs: list[str] = field(default_factory=list[str])
    predictions: list[str] = field(default_factory=list[str])
    open_predictions: list[str] = field(default_factory=list[str])
    notes: list[str] = field(default_factory=list[str])
    sources: list[str] = field(default_factory=list[str])
    bookmarks: list[str] = field(default_factory=list[str])
    issues: list[str] = field(default_factory=list[str])
    principles: list[str] = field(default_factory=list[str])
    metrics: list[str] = field(default_factory=list[str])
    commitment_open: bool = False

    def ideas_in(self, *stages: str) -> list[str]:
        return [i for i in self.ideas if self.stage.get(i) in stages]

    def linkable(self) -> list[str]:
        return [*self.notes, *self.ideas, *self.sources, *self.predictions]

    def all_ids(self) -> list[str]:
        return [*self.linkable(), *self.issues, *self.principles]


def _remove(items: list[str], value: str | None) -> None:
    if value in items:
        items.remove(value)


def parse_view(journal: str) -> View:
    view = View()
    for line in journal.splitlines():
        if not line.strip():
            continue
        obj = as_object(json.loads(line), "journal line")
        kind = str(obj.get("kind", ""))
        raw_id = obj.get("id")
        ident = raw_id if isinstance(raw_id, str) else None
        match kind:
            case "idea" if ident:
                view.ideas.append(ident)
                view.stage[ident] = "Seed"
            case "stage" if ident:
                view.stage[ident] = str(obj.get("text", ""))
            case "ship" if ident:
                view.stage[ident] = "Tend"
            case "retire" if ident:
                view.stage[ident] = "Retired"
            case "req" if ident:
                view.reqs.append(ident)
            case "cut":
                _remove(view.reqs, ident)
            case "predict" if ident:
                view.predictions.append(ident)
                view.open_predictions.append(ident)
            case "result":
                _remove(view.open_predictions, ident)
            case "note" if ident:
                view.notes.append(ident)
            case "source" if ident:
                view.sources.append(ident)
            case "bookmark" if ident:
                view.bookmarks.append(ident)
            case "unbookmark":
                _remove(view.bookmarks, ident)
            case "issue" if ident:
                view.issues.append(ident)
            case "principle" if ident:
                view.principles.append(ident)
            case "log" | "spec" if ident:
                if ident not in view.metrics:
                    view.metrics.append(ident)
            case "commit":
                view.commitment_open = True
            case "kept" | "missed":
                view.commitment_open = False
            case _:
                pass
    return view


class GeneratedSteps:
    """A StepSource: each call reads the journal the oracle wrote after the previous step."""

    def __init__(self, rng: random.Random, count: int) -> None:
        self._rng = rng
        self._left = count
        offset = timedelta(hours=rng.choice(OFFSET_HOURS))
        self._now = START.replace(tzinfo=timezone(offset))
        self._first = True
        self._builders: list[tuple[float, Builder]] = [
            (8, self._idea),
            (4, self._for),
            (4, self._goal),
            (5, self._req),
            (2, self._cut),
            (10, self._advance),
            (3, self._bookmark),
            (1, self._unbookmark),
            (1, self._bookmarks),
            (2, self._adopt),
            (3, self._home),
            (3, self._watch),
            (1, self._unwatch),
            (1, self._watches),
            (3, self._spent),
            (4, self._ship),
            (3, self._used),
            (2, self._tend),
            (1, self._retire),
            (3, self._source),
            (6, self._note),
            (2, self._link),
            (2, self._notes),
            (1, self._tags),
            (1, self._sources),
            (4, self._show),
            (5, self._predict),
            (4, self._result),
            (2, self._predictions),
            (8, self._log),
            (2, self._spec),
            (3, self._stats),
            (3, self._issue),
            (2, self._diagnose),
            (3, self._principle),
            (1, self._issues),
            (1, self._principles),
            (3, self._commit),
            (2, self._kept),
            (1, self._missed),
            (2, self._review),
            (1, self._path),
            (1, self._help),
            (1, self._pipeline),
            (1, self._next),
            (1, self._ideas),
            (1, self._unknown),
        ]

    def next_step(self, journal: str) -> Step | None:
        if self._left == 0:
            return None
        self._left -= 1
        if not self._first:
            self._tick()
        self._first = False
        view = parse_view(journal)
        builder = self._rng.choices(
            [b for _, b in self._builders], weights=[w for w, _ in self._builders]
        )[0]
        if not view.ideas and self._rng.random() < 0.7:
            builder = self._idea
        argv = builder(view)
        stdin = ""
        if self._rng.random() < 0.06:
            spaced = [i for i, a in enumerate(argv) if i > 0 and " " in a]
            if spaced:
                i = self._rng.choice(spaced)
                stdin = argv[i] + "\n"
                argv[i] = "-"
        return Step(now=self._now.isoformat(), argv=tuple(argv), stdin=stdin)

    def _tick(self) -> None:
        roll = self._rng.random()
        if roll < 0.05:
            delta = timedelta(0)
        elif roll < 0.55:
            delta = timedelta(minutes=self._rng.randint(1, 30))
        elif roll < 0.80:
            delta = timedelta(hours=self._rng.randint(1, 12))
        elif roll < 0.95:
            delta = timedelta(days=self._rng.randint(1, 3))
        else:
            delta = timedelta(days=self._rng.randint(4, 10))
        self._now += delta

    # ---- values ----

    def _phrase(self) -> str:
        r = self._rng.random()
        if r < 0.02:
            return ""
        if r < 0.05:
            return 'café ≥ 100 "quoted"'
        words = self._rng.sample(WORDS, self._rng.randint(1, 4))
        return " ".join(words).capitalize()

    def _word(self) -> str:
        return self._rng.choice(WORDS)

    def _id(self, valid: list[str], prefix: str) -> str:
        if valid and self._rng.random() > 0.08:
            return self._rng.choice(valid)
        return f"{prefix}{self._rng.randint(90, 99)}"

    def _idea_id(self, v: View) -> str:
        return self._id(v.ideas, "i")

    def _number(self, low: int, high: int, bad: list[str]) -> str:
        if self._rng.random() < 0.06:
            return self._rng.choice(bad)
        return str(self._rng.randint(low, high))

    def _value(self) -> str:
        if self._rng.random() < 0.05:
            return self._rng.choice(["x", "1e3", "NaN"])
        return f"{self._rng.uniform(-20, 300):.2f}"

    def _maybe(self, p: float, value: str) -> list[str]:
        return [value] if self._rng.random() < p else []

    # ---- commands (each returns argv without the stdin "-" rewrite) ----

    def _idea(self, v: View) -> list[str]:
        return ["idea", self._phrase() or "Idea"]

    def _for(self, v: View) -> list[str]:
        return ["for", self._idea_id(v), self._phrase()]

    def _goal(self, v: View) -> list[str]:
        op = self._rng.choices(
            ["at-least", "at-most", "between", "sideways"], weights=[5, 3, 3, 1]
        )[0]
        if op == "between":
            low = self._rng.randint(1, 80)
            high = low + self._rng.randint(0, 80)
            if self._rng.random() < 0.05:
                low, high = high, low
            numbers = [str(low), str(high)]
        else:
            numbers = [str(self._rng.randint(10, 200))]
        return [
            "goal",
            self._idea_id(v),
            self._phrase() or "Goal",
            self._rng.choice(METRICS),
            op,
            *numbers,
        ]

    def _req(self, v: View) -> list[str]:
        argv = ["req", self._idea_id(v), self._phrase(), self._word().capitalize(), self._phrase()]
        if self._rng.random() < 0.07:
            argv = argv[:-1]
        return argv

    def _cut(self, v: View) -> list[str]:
        return ["cut", self._id(v.reqs, "r"), self._phrase() or "Not needed"]

    def _advance(self, v: View) -> list[str]:
        argv = ["advance", self._idea_id(v)]
        if self._rng.random() < 0.6:
            argv.append(self._number(0, 30, ["-2", "x"]))
        return argv

    def _bookmark(self, v: View) -> list[str]:
        url_roll = self._rng.random()
        if url_roll < 0.06:
            url = "ftp://example.org/file"
        elif url_roll < 0.09:
            url = "not a url"
        else:
            url = f"https://example.org/{self._word()}{self._rng.randint(1, 30)}"
        argv = ["bookmark", self._phrase() or "Link", url]
        if self._rng.random() < 0.5:
            argv.append(self._phrase())
        if self._rng.random() < 0.4:
            argv.append("+" + self._word())
        return argv

    def _unbookmark(self, v: View) -> list[str]:
        return ["unbookmark", self._id(v.bookmarks, "b")]

    def _bookmarks(self, v: View) -> list[str]:
        return ["bookmarks"]

    def _adopt(self, v: View) -> list[str]:
        return ["adopt", self._rng.choice(FOLDERS)]

    def _home(self, v: View) -> list[str]:
        return ["home", self._idea_id(v), self._rng.choice(FOLDERS)]

    def _watch(self, v: View) -> list[str]:
        argv = ["watch", self._idea_id(v), self._rng.choice(PATTERNS)]
        roll = self._rng.random()
        if roll < 0.3:
            argv.append("use")
        elif roll < 0.33:
            argv.append("bogus")
        return argv

    def _unwatch(self, v: View) -> list[str]:
        return ["unwatch", self._idea_id(v), self._rng.choice(PATTERNS)]

    def _watches(self, v: View) -> list[str]:
        return ["watches"]

    def _spent(self, v: View) -> list[str]:
        minutes = self._number(5, 600, ["-5", "0", "x"])
        return ["spent", self._idea_id(v), minutes]

    def _ship(self, v: View) -> list[str]:
        return ["ship", self._idea_id(v), self._phrase() or "Shipped thing"]

    def _used(self, v: View) -> list[str]:
        return ["used", self._idea_id(v), *self._maybe(0.6, self._phrase())]

    def _tend(self, v: View) -> list[str]:
        return ["tend", self._idea_id(v), self._phrase() or "Upkeep"]

    def _retire(self, v: View) -> list[str]:
        return ["retire", self._idea_id(v), self._phrase() or "Not worth it"]

    def _source(self, v: View) -> list[str]:
        url = (
            [f"https://example.org/src{self._rng.randint(1, 9)}"]
            if self._rng.random() < 0.5
            else []
        )
        return ["source", self._phrase() or "Some citation", *url]

    def _note(self, v: View) -> list[str]:
        argv = ["note", self._phrase() or "A thought"]
        for _ in range(self._rng.choice([0, 0, 1, 1, 2])):
            pool = v.linkable()
            if not pool:
                break
            ref = self._id(pool, "n")
            if ref.startswith("s") and self._rng.random() < 0.4:
                ref = f"{ref}:p.{self._rng.randint(1, 99)}"
            argv.append(ref)
        if self._rng.random() < 0.3:
            argv.append("+" + self._word().capitalize())
        if self._rng.random() < 0.02:
            argv.append("+")
        return argv

    def _link(self, v: View) -> list[str]:
        return ["link", self._id(v.notes, "n"), self._id(v.linkable(), "n")]

    def _notes(self, v: View) -> list[str]:
        roll = self._rng.random()
        if roll < 0.4:
            return ["notes"]
        if roll < 0.7:
            return ["notes", "+" + self._word()]
        return ["notes", self._word()]

    def _tags(self, v: View) -> list[str]:
        return ["tags"]

    def _sources(self, v: View) -> list[str]:
        return ["sources"]

    def _show(self, v: View) -> list[str]:
        return ["show", self._id(v.all_ids() + v.bookmarks, "n")]

    def _predict(self, v: View) -> list[str]:
        percent = self._number(1, 99, ["0", "100", "abc", "-3", "50.5"])
        return [
            "predict",
            self._phrase() or "A claim",
            percent,
            *self._maybe(0.6, self._idea_id(v)),
        ]

    def _result(self, v: View) -> list[str]:
        verdict = self._rng.choice(["right", "wrong"]) if self._rng.random() < 0.9 else "maybe"
        return [
            "result",
            self._id(v.open_predictions, "h"),
            verdict,
            self._phrase() or "It happened",
        ]

    def _predictions(self, v: View) -> list[str]:
        return ["predictions"]

    def _log(self, v: View) -> list[str]:
        metric = self._rng.choice(METRICS) if self._rng.random() > 0.03 else "vinci.findings"
        argv = ["log", metric, self._value()]
        if self._rng.random() < 0.3:
            argv.append(self._phrase())
        return argv

    def _spec(self, v: View) -> list[str]:
        low = self._rng.randint(0, 80)
        high = low + self._rng.randint(1, 120)
        if self._rng.random() < 0.05:
            low, high = high, low
        return ["spec", self._rng.choice(METRICS), str(low), str(high)]

    def _stats(self, v: View) -> list[str]:
        if self._rng.random() < 0.5:
            return ["stats"]
        return ["stats", self._rng.choice([*METRICS, *v.metrics, "nothing"])]

    def _issue(self, v: View) -> list[str]:
        argv = ["issue", self._phrase() or "Something broke"]
        argv.extend(self._maybe(0.6, self._idea_id(v)))
        argv.extend(self._maybe(0.3, "big"))
        argv.extend(self._maybe(0.25, self._id(v.principles, "p")))
        return argv

    def _diagnose(self, v: View) -> list[str]:
        return ["diagnose", self._id(v.issues, "x"), self._phrase() or "Root cause"]

    def _principle(self, v: View) -> list[str]:
        return [
            "principle",
            self._phrase() or "Do the thing",
            *self._maybe(0.6, self._id(v.issues, "x")),
        ]

    def _issues(self, v: View) -> list[str]:
        return ["issues"]

    def _principles(self, v: View) -> list[str]:
        return ["principles"]

    def _commit(self, v: View) -> list[str]:
        return ["commit", self._phrase() or "One thing", *self._maybe(0.5, self._idea_id(v))]

    def _kept(self, v: View) -> list[str]:
        return ["kept"]

    def _missed(self, v: View) -> list[str]:
        return ["missed", self._phrase() or "Ran out of time"]

    def _review(self, v: View) -> list[str]:
        return ["review"]

    def _path(self, v: View) -> list[str]:
        return ["path"]

    def _help(self, v: View) -> list[str]:
        return [self._rng.choice(["help", "--help", "-h"])]

    def _pipeline(self, v: View) -> list[str]:
        return ["pipeline"]

    def _next(self, v: View) -> list[str]:
        return ["next"]

    def _ideas(self, v: View) -> list[str]:
        return ["ideas"]

    def _unknown(self, v: View) -> list[str]:
        return [self._rng.choice(["frobnicate", "ship-it", "Idea"])]


def generated_steps(seed: str, count: int) -> GeneratedSteps:
    return GeneratedSteps(random.Random(seed), count)
