"""The fold over the journal: everything vinci knows right now, rebuilt on every run.

The lists keep journal order, because commands print in that order.

A string field holds "" only when it is filled from a required journal field. Optional
fields are None.

Each lookup returns an index into the list, or -1 when nothing matches. A command that needs
the item raises when the lookup finds nothing.

Port of mojo/src/state.mojo.
"""

from __future__ import annotations

from dataclasses import dataclass, field, replace
from typing import Optional

from .journal import Event
from .jsonline import ascii_lower
from .text import num
from .timeutil import Instant, minutes_to_ticks

# Stage codes, in pipeline order.
SEED = 0
PREPARE = 1
INCUBATE = 2
BUILD = 3
TEND = 4
RETIRED = 5

FINDINGS_METRIC = "vinci.findings"


def stage_name(stage: int) -> str:
    if stage == SEED:
        return "Seed"
    if stage == PREPARE:
        return "Prepare"
    if stage == INCUBATE:
        return "Incubate"
    if stage == BUILD:
        return "Build"
    if stage == TEND:
        return "Tend"
    return "Retired"


def stage_code(name: str) -> int:
    """Enum.Parse<Stage>: the exact names only, case-sensitive."""
    codes = {
        "Seed": SEED,
        "Prepare": PREPARE,
        "Incubate": INCUBATE,
        "Build": BUILD,
        "Tend": TEND,
        "Retired": RETIRED,
    }
    if name not in codes:
        raise ValueError("journal: unknown stage " + name)
    return codes[name]


def file_name(path: str) -> str:
    """Path.GetFileName on Linux: everything after the last slash, possibly empty."""
    return path[path.rfind("/") + 1 :]


def home_pattern(home: str) -> str:
    """What a home folder watches for: its name as an editor shows it."""
    return "- " + file_name(home) + " -"


def first_with_prefix(refs: list[str], prefix: str) -> Optional[str]:
    """The first reference whose first character is prefix. An empty reference raises."""
    for r in refs:
        if len(r) == 0:
            raise ValueError("journal: empty reference")
        if r[0] == prefix:
            return r
    return None


def equals_ignore_case(a: str, b: str) -> bool:
    """OrdinalIgnoreCase for ASCII. Non-ASCII letters must match exactly here."""
    return ascii_lower(a) == ascii_lower(b)


@dataclass
class StageAt:
    stage: int
    at: Instant


@dataclass
class TimedText:
    at: Instant
    what: str


@dataclass
class MetricPoint:
    at: Instant
    value: float


@dataclass
class Spec:
    lo: float
    hi: float


@dataclass
class CommitKey:
    idea_id: str
    hash: str


@dataclass
class Watch:
    """Time in a window whose title contains pattern is work on the idea, or the idea
    being used when use is set."""

    idea_id: str
    pattern: str
    use: bool


@dataclass
class Idea:
    id: str
    title: str
    born: Instant
    stage: int
    for_: Optional[str]
    goal: Optional[str]
    goal_metric: Optional[str]
    goal_min: Optional[float]
    goal_max: Optional[float]
    last_touched: Instant
    incubate_until: Optional[Instant]
    touch: int  # ticks
    touch_in: list[int]  # ticks per stage, indexed by stage code
    stage_log: list[StageAt]
    ships: list[TimedText]
    uses: list[Instant]
    home: Optional[str]
    work: list[TimedText]
    retired_why: Optional[str]

    def enter(self, stage: int, at: Instant) -> None:
        if stage == self.stage:
            return
        self.stage = stage
        self.stage_log.append(StageAt(stage, at))

    def add_touch(self, ticks: int) -> None:
        """Work is counted against the stage the idea is in when it is recorded."""
        self.touch += ticks
        self.touch_in[self.stage] = self.touch_in[self.stage] + ticks

    def entered(self, stage: int) -> bool:
        return any(entry.stage == stage for entry in self.stage_log)

    def time_in(self, stage: int, now: Instant) -> int:
        """Ticks the idea has sat in a stage, working or waiting."""
        total = 0
        n = len(self.stage_log)
        for i in range(n):
            if self.stage_log[i].stage != stage:
                continue
            until = now.utc if i + 1 >= n else self.stage_log[i + 1].at.utc
            total += until - self.stage_log[i].at.utc
        return total

    def goal_target(self) -> str:
        """The goal as text: a range, a floor, or a ceiling."""
        if self.goal_min is not None and self.goal_max is not None:
            return num(self.goal_min) + ".." + num(self.goal_max)
        if self.goal_min is not None:
            return "≥ " + num(self.goal_min)
        if self.goal_max is not None:
            return "≤ " + num(self.goal_max)
        raise ValueError("journal: goal of " + self.id + " has no number")


@dataclass
class Requirement:
    id: str
    idea_id: str
    text: str
    who: str
    why: str
    cut_why: Optional[str]

    def live(self) -> bool:
        return self.cut_why is None


@dataclass
class Note:
    """Where is the place inside the source: a page, a chapter, a timestamp."""

    id: str
    at: Instant
    text: str
    refs: list[str]
    tags: list[str]
    where_: Optional[str]


@dataclass
class Source:
    id: str
    at: Instant
    citation: str
    url: Optional[str]


@dataclass
class Issue:
    id: str
    at: Instant
    text: str
    idea_id: Optional[str]
    stage: Optional[int]
    big: bool
    broke_id: Optional[str]
    root_cause: Optional[str]
    principle_id: Optional[str]


@dataclass
class Principle:
    id: str
    at: Instant
    text: str
    issue_id: Optional[str]
    stage: Optional[int]


@dataclass
class Prediction:
    """A claim with a confidence, written before the test. right stays None until the
    result is in."""

    id: str
    at: Instant
    text: str
    confidence: float
    idea_id: Optional[str]
    right: Optional[bool]
    outcome: Optional[str]


@dataclass
class Commitment:
    at: Instant
    text: str
    idea_id: Optional[str]
    kept: Optional[bool]


@dataclass
class Bookmark:
    id: str
    at: Instant
    title: str
    url: str
    why: Optional[str]
    tags: list[str]


@dataclass
class MemoryRecord:
    """A memory-kind line: remember, memory, read or exchange (docs/memory.md).

    The fold keeps these as plain lists for recall to read. No existing command reads them.
    Fields follow the Event they came from: text, refs, value, who, why, url, where and tags.
    """

    kind: str
    id: str
    at: Instant
    text: str
    refs: list[str]
    value: Optional[float]
    who: Optional[str]
    why: Optional[str]
    url: Optional[str]
    where_: Optional[str]
    tags: list[str]


@dataclass
class Metric:
    name: str
    points: list[MetricPoint]
    spec: Optional[Spec]


def empty_touch_in() -> list[int]:
    return [0] * (RETIRED + 1)


def new_idea(id: str, title: str, born: Instant) -> Idea:
    return Idea(
        id=id,
        title=title,
        born=born,
        stage=SEED,
        for_=None,
        goal=None,
        goal_metric=None,
        goal_min=None,
        goal_max=None,
        last_touched=born,
        incubate_until=None,
        touch=0,
        touch_in=empty_touch_in(),
        stage_log=[StageAt(SEED, born)],
        ships=[],
        uses=[],
        home=None,
        work=[],
        retired_why=None,
    )


def required_value(value: Optional[float]) -> float:
    """A field the event must carry: a missing one is an error."""
    if value is None:
        raise ValueError("journal: event is missing its value")
    return value


def _index_by(items: list, key: str, id: str) -> int:
    for i, item in enumerate(items):
        if getattr(item, key) == id:
            return i
    return -1


@dataclass
class State:
    ideas: list[Idea] = field(default_factory=list)
    requirements: list[Requirement] = field(default_factory=list)
    notes: list[Note] = field(default_factory=list)
    sources: list[Source] = field(default_factory=list)
    issues: list[Issue] = field(default_factory=list)
    principles: list[Principle] = field(default_factory=list)
    metrics: list[Metric] = field(default_factory=list)
    predictions: list[Prediction] = field(default_factory=list)
    commitments: list[Commitment] = field(default_factory=list)
    bookmarks: list[Bookmark] = field(default_factory=list)
    watches: list[Watch] = field(default_factory=list)
    watching_since: Optional[Instant] = None
    synced_to: Optional[Instant] = None
    commits: list[CommitKey] = field(default_factory=list)
    # Bookmarks ever made, kept or not, so an id is never used twice.
    bookmarks_made: int = 0
    # Memory-kind lines in journal order (see MemoryRecord). Nothing reads them yet.
    memory_records: list[MemoryRecord] = field(default_factory=list)

    # ---- lookups: an index into the list, or -1 ----

    def idea_index(self, id: str) -> int:
        return _index_by(self.ideas, "id", id)

    def requirement_index(self, id: str) -> int:
        return _index_by(self.requirements, "id", id)

    def note_index(self, id: str) -> int:
        return _index_by(self.notes, "id", id)

    def source_index(self, id: str) -> int:
        return _index_by(self.sources, "id", id)

    def issue_index(self, id: str) -> int:
        return _index_by(self.issues, "id", id)

    def principle_index(self, id: str) -> int:
        return _index_by(self.principles, "id", id)

    def metric_index(self, name: str) -> int:
        return _index_by(self.metrics, "name", name)

    def prediction_index(self, id: str) -> int:
        return _index_by(self.predictions, "id", id)

    def open_commitment_index(self) -> int:
        """The first commitment nobody has answered for yet, or -1."""
        for i, c in enumerate(self.commitments):
            if c.kept is None:
                return i
        return -1

    def require_idea(self, id: str) -> int:
        i = self.idea_index(id)
        if i < 0:
            raise ValueError("journal: event names an idea that was never created: " + id)
        return i

    def require_requirement(self, id: str) -> int:
        i = self.requirement_index(id)
        if i < 0:
            raise ValueError("journal: event names a requirement that does not exist: " + id)
        return i

    def require_note(self, id: str) -> int:
        i = self.note_index(id)
        if i < 0:
            raise ValueError("journal: event names a note that does not exist: " + id)
        return i

    def require_issue(self, id: str) -> int:
        i = self.issue_index(id)
        if i < 0:
            raise ValueError("journal: event names an issue that does not exist: " + id)
        return i

    def require_prediction(self, id: str) -> int:
        i = self.prediction_index(id)
        if i < 0:
            raise ValueError("journal: event names a prediction that does not exist: " + id)
        return i

    def has(self, id: str) -> bool:
        """Whether the id names an idea, note, source, issue, requirement, principle or
        prediction."""
        return (
            self.idea_index(id) >= 0
            or self.note_index(id) >= 0
            or self.source_index(id) >= 0
            or self.issue_index(id) >= 0
            or self.requirement_index(id) >= 0
            or self.principle_index(id) >= 0
            or self.prediction_index(id) >= 0
        )

    def next_id(self, prefix: str) -> str:
        """The next id with the prefix letter: i1, r2, n3 and so on."""
        counts = {
            "i": len(self.ideas),
            "r": len(self.requirements),
            "n": len(self.notes),
            "s": len(self.sources),
            "x": len(self.issues),
            "p": len(self.principles),
            "h": len(self.predictions),
            "b": self.bookmarks_made,
        }
        if prefix not in counts:
            raise ValueError("journal: no id prefix " + prefix)
        return prefix + str(counts[prefix] + 1)

    def notes_about(self, id: str) -> list[int]:
        """Indices of the notes that refer to id."""
        return [i for i, n in enumerate(self.notes) if id in n.refs]

    def has_commit(self, idea_id: str, hash: str) -> bool:
        return any(c.idea_id == idea_id and c.hash == hash for c in self.commits)

    def breaks(self, principle_id: str) -> int:
        """How many issues broke this principle."""
        return sum(1 for x in self.issues if x.broke_id == principle_id)

    def watched(self, idea_index: int) -> bool:
        """True when work on the idea is counted without anyone recording it."""
        if self.ideas[idea_index].home is not None:
            return True
        for w in self.watches:
            if w.idea_id == self.ideas[idea_index].id and not w.use:
                return True
        return False

    def all_watches(self) -> list[Watch]:
        """Every watch in force: the ones recorded, and one implied by each home. Watches
        on retired ideas are left out."""
        out = [replace(w) for w in self.watches]
        for idea in self.ideas:
            if idea.home is not None:
                out.append(Watch(idea.id, home_pattern(idea.home), False))
        kept: list[Watch] = []
        for w in out:
            i = self.require_idea(w.idea_id)
            if self.ideas[i].stage != RETIRED:
                kept.append(replace(w))
        return kept

    def goal_met(self, idea_index: int) -> Optional[bool]:
        """None when the goal's metric has never been measured."""
        idea = self.ideas[idea_index]
        if idea.goal_metric is None:
            return None
        m = self.metric_index(idea.goal_metric)
        if m < 0 or len(self.metrics[m].points) == 0:
            return None
        latest = self.metrics[m].points[-1].value
        lo_ok = True
        if idea.goal_min is not None:
            lo_ok = latest >= idea.goal_min
        hi_ok = True
        if idea.goal_max is not None:
            hi_ok = latest <= idea.goal_max
        return lo_ok and hi_ok

    # ---- the fold ----

    def metric_or_new(self, name: str) -> int:
        i = self.metric_index(name)
        if i < 0:
            self.metrics.append(Metric(name, [], None))
            i = len(self.metrics) - 1
        return i

    def touch_idea_of(self, id: str, at: Instant) -> None:
        """Credit the idea a fact points at with its last touch. A requirement counts for
        its idea."""
        i = self.idea_index(id)
        if i < 0:
            r = self.requirement_index(id)
            if r >= 0:
                i = self.idea_index(self.requirements[r].idea_id)
        if i >= 0:
            self.ideas[i].last_touched = at

    def apply(self, e: Event) -> None:
        kind = e.kind
        id = e.id if e.id is not None else ""
        text = e.text if e.text is not None else ""
        refs = list(e.refs) if e.refs is not None else []
        at = e.at

        if kind == "idea":
            self.ideas.append(new_idea(id, text, at))
        elif kind == "for":
            i = self.require_idea(id)
            self.ideas[i].for_ = e.text
        elif kind == "goal":
            i = self.require_idea(id)
            self.ideas[i].goal = e.text
            self.ideas[i].goal_metric = e.metric
            self.ideas[i].goal_min = e.value
            self.ideas[i].goal_max = e.hi
        elif kind == "req":
            if len(refs) == 0:
                raise ValueError("journal: req has no idea")
            self.requirements.append(
                Requirement(
                    id,
                    refs[0],
                    text,
                    e.who if e.who is not None else "",
                    e.why if e.why is not None else "",
                    None,
                )
            )
        elif kind == "cut":
            r = self.require_requirement(id)
            self.requirements[r].cut_why = e.text
        elif kind == "stage":
            i = self.require_idea(id)
            self.ideas[i].enter(stage_code(text), at)
            if self.ideas[i].stage == INCUBATE:
                days = 1.0
                if e.value is not None:
                    days = e.value
                self.ideas[i].incubate_until = at.add_days(days)
        elif kind == "watch":
            if self.watching_since is None:
                self.watching_since = at
            self.watches.append(Watch(id, text, (e.value if e.value is not None else 0.0) == 1.0))
        elif kind == "unwatch":
            self.watches = [
                w for w in self.watches if not (w.idea_id == id and equals_ignore_case(w.pattern, text))
            ]
        elif kind == "bookmark":
            self.bookmarks_made += 1
            if e.url is None:
                raise ValueError("journal: bookmark has no url")
            self.bookmarks.append(
                Bookmark(
                    id,
                    at,
                    text,
                    e.url,
                    e.why,
                    list(e.tags) if e.tags is not None else [],
                )
            )
        elif kind == "unbookmark":
            self.bookmarks = [b for b in self.bookmarks if b.id != id]
        elif kind == "home":
            if self.watching_since is None:
                self.watching_since = at
            i = self.require_idea(id)
            self.ideas[i].home = e.text
        elif kind == "work":
            i = self.require_idea(id)
            self.ideas[i].work.append(TimedText(at, text))
            # The same pair twice is kept once.
            hash = e.where_ if e.where_ is not None else ""
            if not self.has_commit(id, hash):
                self.commits.append(CommitKey(id, hash))
        elif kind == "sync":
            self.synced_to = at
        elif kind == "spent":
            i = self.require_idea(id)
            self.ideas[i].add_touch(minutes_to_ticks(required_value(e.value)))
        elif kind == "ship":
            i = self.require_idea(id)
            self.ideas[i].ships.append(TimedText(at, text))
            self.ideas[i].enter(TEND, at)
        elif kind == "used":
            i = self.require_idea(id)
            self.ideas[i].uses.append(at)
        elif kind == "retire":
            i = self.require_idea(id)
            self.ideas[i].enter(RETIRED, at)
            self.ideas[i].retired_why = e.text
        elif kind == "source":
            self.sources.append(Source(id, at, text, e.url))
        elif kind == "note":
            self.notes.append(
                Note(
                    id,
                    at,
                    text,
                    refs,
                    list(e.tags) if e.tags is not None else [],
                    e.where_,
                )
            )
        elif kind == "link":
            n = self.require_note(id)
            for r in refs:
                self.notes[n].refs.append(r)
        elif kind == "log":
            m = self.metric_or_new(id)
            self.metrics[m].points.append(MetricPoint(at, required_value(e.value)))
        elif kind == "spec":
            m = self.metric_or_new(id)
            if e.hi is None:
                raise ValueError("journal: spec has no high limit")
            self.metrics[m].spec = Spec(required_value(e.value), e.hi)
        elif kind == "issue":
            about = first_with_prefix(refs, "i")
            about_stage: Optional[int] = None
            if about is not None:
                i = self.require_idea(about)
                about_stage = self.ideas[i].stage
            big = (e.value if e.value is not None else 0.0) == 1.0
            broke = first_with_prefix(refs, "p")
            self.issues.append(Issue(id, at, text, about, about_stage, big, broke, None, None))
        elif kind == "diagnose":
            x = self.require_issue(id)
            self.issues[x].root_cause = e.text
        elif kind == "principle":
            issue_index = -1
            if len(refs) > 0:
                issue_index = self.issue_index(refs[0])
            issue_id: Optional[str] = None
            issue_stage: Optional[int] = None
            if issue_index >= 0:
                issue_id = self.issues[issue_index].id
                issue_stage = self.issues[issue_index].stage
            self.principles.append(Principle(id, at, text, issue_id, issue_stage))
            if issue_index >= 0:
                self.issues[issue_index].principle_id = id
        elif kind == "predict":
            about_idea = refs[0] if len(refs) > 0 else None
            self.predictions.append(
                Prediction(id, at, text, required_value(e.value), about_idea, None, None)
            )
        elif kind == "result":
            p = self.require_prediction(id)
            self.predictions[p].right = (e.value if e.value is not None else 0.0) == 1.0
            self.predictions[p].outcome = e.text
        elif kind == "commit":
            about_idea = refs[0] if len(refs) > 0 else None
            self.commitments.append(Commitment(at, text, about_idea, None))
        elif kind in ("kept", "missed"):
            c = self.open_commitment_index()
            if c < 0:
                raise ValueError("journal: " + kind + " with no open commitment")
            self.commitments[c].kept = kind == "kept"
        elif kind == "review":
            m = self.metric_or_new(FINDINGS_METRIC)
            self.metrics[m].points.append(MetricPoint(at, required_value(e.value)))
        elif kind in ("remember", "memory", "read", "exchange"):
            self.memory_records.append(
                MemoryRecord(
                    kind,
                    id,
                    at,
                    text,
                    refs,
                    e.value,
                    e.who,
                    e.why,
                    e.url,
                    e.where_,
                    list(e.tags) if e.tags is not None else [],
                )
            )
        # "tend" changes nothing but when the idea was last touched.
        # Unknown kinds are skipped so an older build can read a newer journal.

        # Metric names are free text, so they must not be read as ids.
        if kind in ("log", "spec"):
            return
        if e.id is not None:
            self.touch_idea_of(id, at)
        for r in refs:
            self.touch_idea_of(r, at)


def state_from(events: list[Event]) -> State:
    """State.From: fold every event in journal order."""
    state = State()
    for e in events:
        state.apply(e)
    return state
