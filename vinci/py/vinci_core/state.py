"""
State - The fold over the journal: everything vinci knows right now.

Everything is rebuilt on every run from the journal. The lists keep journal order.
"""

from typing import Optional, List, Dict
from dataclasses import dataclass, field
from .journal import Event
from .text import (
    ascii_lower, num, pad_right, decimal_digits, round_half_up, fixed_text
)
from .timeutil import Instant, minutes_to_ticks


# Stage codes, in pipeline order
SEED = 0
PREPARE = 1
INCUBATE = 2
BUILD = 3
TEND = 4
RETIRED = 5

FINDINGS_METRIC = "vinci.findings"


def stage_name(stage: int) -> str:
    """Convert stage code to name."""
    names = ["Seed", "Prepare", "Incubate", "Build", "Tend", "Retired"]
    return names[stage]


def stage_code(name: str) -> int:
    """Convert stage name to code."""
    code_map = {
        "Seed": SEED,
        "Prepare": PREPARE,
        "Incubate": INCUBATE,
        "Build": BUILD,
        "Tend": TEND,
        "Retired": RETIRED,
    }
    return code_map[name]


def empty_touch_in() -> List[int]:
    """Create an empty touch_in list for all stages."""
    return [0] * (RETIRED + 1)


def new_idea(id: str, title: str, born: Instant) -> 'Idea':
    """Create a new idea in Seed stage."""
    log = [StageAt(SEED, born)]
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
        stage_log=log,
        ships=[],
        uses=[],
        home=None,
        work=[],
        retired_why=None,
    )


@dataclass
class StageAt:
    """A stage transition with timestamp."""
    stage: int
    at: Instant


@dataclass
class TimedText:
    """Text with a timestamp."""
    at: Instant
    what: str


@dataclass
class MetricPoint:
    """A metric measurement point."""
    at: Instant
    value: float


@dataclass
class Spec:
    """Metric specification: lo and hi bounds."""
    lo: float
    hi: float


@dataclass
class CommitKey:
    """Key for deduplicating commits."""
    idea_id: str
    hash: str


@dataclass
class Watch:
    """Time in a window whose title contains pattern is work on the idea."""
    idea_id: str
    pattern: str
    use: bool = False


@dataclass
class Idea:
    """An idea tracked through the pipeline."""
    id: str
    title: str
    born: Instant
    stage: int
    for_: Optional[str] = None
    goal: Optional[str] = None
    goal_metric: Optional[str] = None
    goal_min: Optional[float] = None
    goal_max: Optional[float] = None
    last_touched: Instant = field(default_factory=lambda: Instant(0, 0))
    incubate_until: Optional[Instant] = None
    touch: int = 0  # ticks
    touch_in: List[int] = field(default_factory=empty_touch_in)
    stage_log: List[StageAt] = field(default_factory=list)
    ships: List[TimedText] = field(default_factory=list)
    uses: List[Instant] = field(default_factory=list)
    home: Optional[str] = None
    work: List[TimedText] = field(default_factory=list)
    retired_why: Optional[str] = None

    def enter(self, stage: int, at: Instant):
        """Record entering a stage."""
        if stage == self.stage:
            return
        self.stage = stage
        self.stage_log.append(StageAt(stage, at))

    def add_touch(self, ticks: int):
        """Add touch time to current stage."""
        self.touch += ticks
        self.touch_in[self.stage] = self.touch_in[self.stage] + ticks

    def entered(self, stage: int) -> bool:
        """Check if we've entered this stage."""
        for entry in self.stage_log:
            if entry.stage == stage:
                return True
        return False

    def time_in(self, stage: int, now: Instant) -> int:
        """Ticks the idea has sat in a stage."""
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
            return f"{num(self.goal_min)}..{num(self.goal_max)}"
        if self.goal_min is not None:
            return f">= {num(self.goal_min)}"
        if self.goal_max is not None:
            return f"<= {num(self.goal_max)}"
        raise ValueError(f"journal: goal of {self.id} has no number")


@dataclass
class Requirement:
    """A requirement with owner and reason."""
    id: str
    idea_id: str
    text: str
    who: str
    why: str
    cut_why: Optional[str] = None

    def live(self) -> bool:
        return self.cut_why is None


@dataclass
class Note:
    """A note with references and tags."""
    id: str
    at: Instant
    text: str
    refs: List[str] = field(default_factory=list)
    tags: List[str] = field(default_factory=list)
    where_: Optional[str] = None


@dataclass
class Source:
    """A source citation."""
    id: str
    at: Instant
    citation: str
    url: Optional[str] = None


@dataclass
class Issue:
    """An issue with its idea, stage, and diagnosis."""
    id: str
    at: Instant
    text: str
    idea_id: Optional[str] = None
    stage: Optional[int] = None
    big: bool = False
    broke_id: Optional[str] = None
    root_cause: Optional[str] = None
    principle_id: Optional[str] = None


@dataclass
class Principle:
    """A principle derived from an issue."""
    id: str
    at: Instant
    text: str
    issue_id: Optional[str] = None
    stage: Optional[int] = None


@dataclass
class Prediction:
    """A prediction with confidence, written before the test."""
    id: str
    at: Instant
    text: str
    confidence: float
    idea_id: Optional[str] = None
    right: Optional[bool] = None
    outcome: Optional[str] = None


@dataclass
class Commitment:
    """A commitment to do something."""
    at: Instant
    text: str
    idea_id: Optional[str] = None
    kept: Optional[bool] = None


@dataclass
class Bookmark:
    """A bookmark with title and tags."""
    id: str
    at: Instant
    title: str
    url: str
    why: Optional[str] = None
    tags: List[str] = field(default_factory=list)


@dataclass
class MemoryRecord:
    """A memory-kind line: remember, memory, read or exchange."""
    kind: str
    id: str
    at: Instant
    text: str
    refs: List[str] = field(default_factory=list)
    value: Optional[float] = None
    who: Optional[str] = None
    why: Optional[str] = None
    url: Optional[str] = None
    where_: Optional[str] = None
    tags: List[str] = field(default_factory=list)


@dataclass
class Metric:
    """A metric with measurements and spec."""
    name: str
    points: List[MetricPoint] = field(default_factory=list)
    spec: Optional[Spec] = None


@dataclass
class State:
    """The complete state folded from the journal."""
    ideas: List[Idea] = field(default_factory=list)
    requirements: List[Requirement] = field(default_factory=list)
    notes: List[Note] = field(default_factory=list)
    sources: List[Source] = field(default_factory=list)
    issues: List[Issue] = field(default_factory=list)
    principles: List[Principle] = field(default_factory=list)
    metrics: List[Metric] = field(default_factory=list)
    predictions: List[Prediction] = field(default_factory=list)
    commitments: List[Commitment] = field(default_factory=list)
    bookmarks: List[Bookmark] = field(default_factory=list)
    watches: List[Watch] = field(default_factory=list)
    watching_since: Optional[Instant] = None
    synced_to: Optional[Instant] = None
    commits: List[CommitKey] = field(default_factory=list)
    bookmarks_made: int = 0
    memory_records: List[MemoryRecord] = field(default_factory=list)

    def __init__(self):
        """Initialize with empty lists."""
        self.ideas = []
        self.requirements = []
        self.notes = []
        self.sources = []
        self.issues = []
        self.principles = []
        self.metrics = []
        self.predictions = []
        self.commitments = []
        self.bookmarks = []
        self.watches = []
        self.watching_since = None
        self.synced_to = None
        self.commits = []
        self.bookmarks_made = 0
        self.memory_records = []

    # ---- Lookups: return index or -1 ----
    
    def idea_index(self, id: str) -> int:
        for i, idea in enumerate(self.ideas):
            if idea.id == id:
                return i
        return -1

    def requirement_index(self, id: str) -> int:
        for i, req in enumerate(self.requirements):
            if req.id == id:
                return i
        return -1

    def note_index(self, id: str) -> int:
        for i, note in enumerate(self.notes):
            if note.id == id:
                return i
        return -1

    def source_index(self, id: str) -> int:
        for i, src in enumerate(self.sources):
            if src.id == id:
                return i
        return -1

    def issue_index(self, id: str) -> int:
        for i, issue in enumerate(self.issues):
            if issue.id == id:
                return i
        return -1

    def principle_index(self, id: str) -> int:
        for i, princ in enumerate(self.principles):
            if princ.id == id:
                return i
        return -1

    def metric_index(self, name: str) -> int:
        for i, metric in enumerate(self.metrics):
            if metric.name == name:
                return i
        return -1

    def prediction_index(self, id: str) -> int:
        for i, pred in enumerate(self.predictions):
            if pred.id == id:
                return i
        return -1

    def open_commitment_index(self) -> int:
        """First commitment nobody has answered for yet."""
        for i, comm in enumerate(self.commitments):
            if comm.kept is None:
                return i
        return -1

    def require_idea(self, id: str) -> int:
        i = self.idea_index(id)
        if i < 0:
            raise ValueError(f"journal: event names an idea that was never created: {id}")
        return i

    def require_requirement(self, id: str) -> int:
        i = self.requirement_index(id)
        if i < 0:
            raise ValueError(f"journal: event names a requirement that does not exist: {id}")
        return i

    def require_note(self, id: str) -> int:
        i = self.note_index(id)
        if i < 0:
            raise ValueError(f"journal: event names a note that does not exist: {id}")
        return i

    def require_issue(self, id: str) -> int:
        i = self.issue_index(id)
        if i < 0:
            raise ValueError(f"journal: event names an issue that does not exist: {id}")
        return i

    def require_prediction(self, id: str) -> int:
        i = self.prediction_index(id)
        if i < 0:
            raise ValueError(f"journal: event names a prediction that does not exist: {id}")
        return i

    def has(self, id: str) -> bool:
        """Whether the id names anything tracked."""
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
        """Generate the next id with the given prefix."""
        count: int
        if prefix == "i":
            count = len(self.ideas)
        elif prefix == "r":
            count = len(self.requirements)
        elif prefix == "n":
            count = len(self.notes)
        elif prefix == "s":
            count = len(self.sources)
        elif prefix == "x":
            count = len(self.issues)
        elif prefix == "p":
            count = len(self.principles)
        elif prefix == "h":
            count = len(self.predictions)
        elif prefix == "b":
            count = self.bookmarks_made
        else:
            raise ValueError(f"journal: no id prefix {prefix}")
        return f"{prefix}{count + 1}"

    def notes_about(self, id: str) -> List[int]:
        """Indices of notes that refer to id."""
        out = []
        for i, note in enumerate(self.notes):
            for r in note.refs:
                if r == id:
                    out.append(i)
                    break
        return out

    def has_commit(self, idea_id: str, hash: str) -> bool:
        for c in self.commits:
            if c.idea_id == idea_id and c.hash == hash:
                return True
        return False

    def breaks(self, principle_id: str) -> int:
        """How many issues broke this principle."""
        count = 0
        for x in self.issues:
            if x.broke_id is not None and x.broke_id == principle_id:
                count += 1
        return count

    def watched(self, idea_index: int) -> bool:
        """True when work on the idea is counted without recording."""
        if self.ideas[idea_index].home is not None:
            return True
        for w in self.watches:
            if w.idea_id == self.ideas[idea_index].id and not w.use:
                return True
        return False

    def all_watches(self) -> List[Watch]:
        """Every watch in force: recorded and home-implied."""
        out = list(self.watches)
        for i, idea in enumerate(self.ideas):
            if idea.home is not None:
                out.append(Watch(
                    idea_id=idea.id,
                    pattern=f"- {file_name(idea.home)} -",
                    use=False,
                ))
        
        # Filter to non-retired ideas
        kept = []
        for w in out:
            idea_idx = self.idea_index(w.idea_id)
            if idea_idx >= 0 and self.ideas[idea_idx].stage != RETIRED:
                kept.append(w)
        return kept

    def goal_met(self, idea_index: int) -> Optional[bool]:
        """None when metric never measured."""
        metric_name = self.ideas[idea_index].goal_metric
        if metric_name is None:
            return None
        m = self.metric_index(metric_name)
        if m < 0 or not self.metrics[m].points:
            return None
        latest = self.metrics[m].points[-1].value
        lo_ok = True
        if self.ideas[idea_index].goal_min is not None:
            lo_ok = latest >= self.ideas[idea_index].goal_min
        hi_ok = True
        if self.ideas[idea_index].goal_max is not None:
            hi_ok = latest <= self.ideas[idea_index].goal_max
        return lo_ok and hi_ok

    # ---- The fold: apply events to build state ----
    
    def metric_or_new(self, name: str) -> int:
        """Get metric index or create new one."""
        i = self.metric_index(name)
        if i < 0:
            self.metrics.append(Metric(name, [], None))
            i = len(self.metrics) - 1
        return i

    def touch_idea_of(self, id: str, at: Instant):
        """Credit an idea with its last touch."""
        i = self.idea_index(id)
        if i < 0:
            # Check requirements
            r = self.requirement_index(id)
            if r >= 0:
                i = self.idea_index(self.requirements[r].idea_id)
        if i >= 0:
            self.ideas[i].last_touched = at

    def apply(self, event: Event):
        """Apply a single event to the state."""
        kind = event.kind
        id = event.id or ""
        text = event.text or ""
        refs = event.refs or []
        at = Instant(event.at, 0)

        if kind == "idea":
            self.ideas.append(new_idea(id, text, at))
        elif kind == "for":
            i = self.require_idea(id)
            self.ideas[i].for_ = event.text
        elif kind == "goal":
            i = self.require_idea(id)
            self.ideas[i].goal = event.text
            self.ideas[i].goal_metric = event.metric or None
            self.ideas[i].goal_min = event.value
            self.ideas[i].goal_max = event.hi
        elif kind == "req":
            if not refs:
                raise ValueError("journal: req has no idea")
            self.requirements.append(Requirement(
                id=id,
                idea_id=refs[0],
                text=text,
                who=event.who or "",
                why=event.why or "",
            ))
        elif kind == "cut":
            r = self.require_requirement(id)
            self.requirements[r].cut_why = event.text
        elif kind == "stage":
            i = self.require_idea(id)
            self.ideas[i].enter(stage_code(text), at)
            if self.ideas[i].stage == INCUBATE:
                days = 1.0
                if event.value is not None:
                    days = float(event.value)
                self.ideas[i].incubate_until = at.add_days(days)
        elif kind == "watch":
            if self.watching_since is None:
                self.watching_since = at
            self.watches.append(Watch(id, text, event.use if event.use is not None else False))
        elif kind == "unwatch":
            kept = [w for w in self.watches 
                    if not (w.idea_id == id and ascii_lower(w.pattern) == ascii_lower(text))]
            self.watches = kept
        elif kind == "bookmark":
            self.bookmarks_made += 1
            if not event.url:
                raise ValueError("journal: bookmark has no url")
            self.bookmarks.append(Bookmark(
                id=id,
                at=at,
                title=text,
                url=event.url,
                why=event.why,
                tags=event.tags or [],
            ))
        elif kind == "unbookmark":
            kept = [b for b in self.bookmarks if b.id != id]
            self.bookmarks = kept
        elif kind == "home":
            if self.watching_since is None:
                self.watching_since = at
            i = self.require_idea(id)
            self.ideas[i].home = event.text
        elif kind == "work":
            i = self.require_idea(id)
            self.ideas[i].work.append(TimedText(at, text))
            hash_val = event.where_ or ""
            if not self.has_commit(id, hash_val):
                self.commits.append(CommitKey(id, hash_val))
        elif kind == "sync":
            self.synced_to = at
        elif kind == "spent":
            i = self.require_idea(id)
            self.ideas[i].add_touch(minutes_to_ticks(float(event.value)))
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
            self.ideas[i].retired_why = event.text
        elif kind == "source":
            self.sources.append(Source(id=id, at=at, citation=text, url=event.url))
        elif kind == "note":
            self.notes.append(Note(
                id=id,
                at=at,
                text=text,
                refs=event.refs or [],
                tags=event.tags or [],
                where_=event.where_,
            ))
        elif kind == "link":
            n = self.require_note(id)
            for r in event.refs or []:
                self.notes[n].refs.append(r)
        elif kind == "log":
            m = self.metric_or_new(id)
            self.metrics[m].points.append(MetricPoint(at, float(event.value)))
        elif kind == "spec":
            m = self.metric_or_new(id)
            if not event.hi:
                raise ValueError("journal: spec has no high limit")
            self.metrics[m].spec = Spec(float(event.value), float(event.hi))
        elif kind == "issue":
            # Find idea reference (starts with 'i')
            about = None
            about_stage = None
            for r in event.refs or []:
                if r and r[0] == 'i':
                    about = r
                    break
            if about:
                i = self.require_idea(about)
                about_stage = self.ideas[i].stage
            
            big = event.big if event.big is not None else False
            broke = None
            for r in event.refs or []:
                if r and r[0] == 'p':
                    broke = r
                    break
            
            self.issues.append(Issue(
                id=id,
                at=at,
                text=text,
                idea_id=about,
                stage=about_stage,
                big=big,
                broke_id=broke,
            ))
        elif kind == "diagnose":
            x = self.require_issue(id)
            self.issues[x].root_cause = event.text
        elif kind == "principle":
            issue_index = -1
            if event.refs and event.refs[0]:
                issue_index = self.issue_index(event.refs[0])
            
            issue_id = None
            issue_stage = None
            if issue_index >= 0:
                issue_id = self.issues[issue_index].id
                issue_stage = self.issues[issue_index].stage
            
            self.principles.append(Principle(
                id=id,
                at=at,
                text=text,
                issue_id=issue_id,
                stage=issue_stage,
            ))
            
            if issue_index >= 0:
                self.issues[issue_index].principle_id = id
        elif kind == "predict":
            about_idea = None
            if event.refs and event.refs[0]:
                about_idea = event.refs[0]
            
            self.predictions.append(Prediction(
                id=id,
                at=at,
                text=text,
                confidence=float(event.value),
                idea_id=about_idea,
            ))
        elif kind == "result":
            p = self.require_prediction(id)
            self.predictions[p].right = event.value == 1.0
            self.predictions[p].outcome = event.text
        elif kind == "commit":
            about_idea = None
            if event.refs and event.refs[0]:
                about_idea = event.refs[0]
            self.commitments.append(Commitment(
                at=at,
                text=text,
                idea_id=about_idea,
                kept=None,
            ))
        elif kind in ("kept", "missed"):
            c = self.open_commitment_index()
            if c < 0:
                raise ValueError(f"journal: {kind} with no open commitment")
            self.commitments[c].kept = (kind == "kept")
        elif kind == "review":
            m = self.metric_or_new(FINDINGS_METRIC)
            self.metrics[m].points.append(MetricPoint(at, float(event.value)))
        elif kind in ("remember", "memory", "read", "exchange"):
            self.memory_records.append(MemoryRecord(
                kind=kind,
                id=id,
                at=at,
                text=text,
                refs=event.refs or [],
                value=event.value,
                who=event.who,
                why=event.why,
                url=event.url,
                where_=event.where_,
                tags=event.tags or [],
            ))

        # Touch tracking
        if kind in ("log", "spec"):
            return
        if event.id:
            self.touch_idea_of(event.id, at)
        for r in event.refs or []:
            self.touch_idea_of(r, at)


def state_from(events: List[Event]) -> State:
    """Fold all events into state."""
    state = State()
    for event in events:
        state.apply(event)
    return state


def file_name(path: str) -> str:
    """Get filename from path (everything after last slash)."""
    import os
    return os.path.basename(path)


def required_value(value: Optional[float]) -> float:
    """A field the event must carry."""
    if value is None:
        raise ValueError("journal: event is missing its value")
    return value
