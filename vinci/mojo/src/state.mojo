# The fold over the journal: everything vinci knows right now, rebuilt on every
# run. The lists keep journal order, because commands print in that order.
#
# A String field holds "" only when it is filled from a required journal field.
# Optional fields are Optional[...].
#
# Each lookup returns an index into the list, or -1 when nothing matches. A command
# that needs the item raises when the lookup finds nothing.

from journal import Event
from jsonline import ascii_lower
from text import ascii_tail, num
from timeutil import Instant, minutes_to_ticks

# Stage codes, in pipeline order.
comptime SEED = 0
comptime PREPARE = 1
comptime INCUBATE = 2
comptime BUILD = 3
comptime TEND = 4
comptime RETIRED = 5

comptime FINDINGS_METRIC = "vinci.findings"


def stage_name(stage: Int) -> String:
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


def stage_code(name: String) raises -> Int:
    """Enum.Parse<Stage>: the exact names only, case-sensitive."""
    if name == "Seed":
        return SEED
    if name == "Prepare":
        return PREPARE
    if name == "Incubate":
        return INCUBATE
    if name == "Build":
        return BUILD
    if name == "Tend":
        return TEND
    if name == "Retired":
        return RETIRED
    raise Error("journal: unknown stage " + name)


def file_name(path: String) -> String:
    """Path.GetFileName on Linux: everything after the last slash, possibly empty.
    """
    var src = path.as_bytes()
    var cut = -1
    for i in range(len(src)):
        if src[i] == 47:  # /
            cut = i
    return ascii_tail(path, cut + 1)


def home_pattern(home: String) -> String:
    """What a home folder watches for: its name as an editor shows it."""
    return "- " + file_name(home) + " -"


def first_with_prefix(
    refs: List[String], prefix: UInt8
) raises -> Optional[String]:
    """The first reference whose first byte is prefix. An empty reference raises.
    """
    for r in refs:
        var b = r.as_bytes()
        if len(b) == 0:
            raise Error("journal: empty reference")
        if b[0] == prefix:
            return r
    return None


def equals_ignore_case(a: String, b: String) -> Bool:
    """OrdinalIgnoreCase for ASCII. Non-ASCII letters must match exactly here.
    """
    return ascii_lower(a) == ascii_lower(b)


@fieldwise_init
struct StageAt(Copyable, Movable):
    var stage: Int
    var at: Instant


@fieldwise_init
struct TimedText(Copyable, Movable):
    var at: Instant
    var what: String


@fieldwise_init
struct MetricPoint(Copyable, Movable):
    var at: Instant
    var value: Float64


@fieldwise_init
struct Spec(Copyable, Movable):
    var lo: Float64
    var hi: Float64


@fieldwise_init
struct CommitKey(Copyable, Movable):
    var idea_id: String
    var hash: String


@fieldwise_init
struct Watch(Copyable, Movable):
    """Time in a window whose title contains pattern is work on the idea, or the
    idea being used when use is set."""

    var idea_id: String
    var pattern: String
    var use: Bool


@fieldwise_init
struct Idea(Copyable, Movable):
    var id: String
    var title: String
    var born: Instant
    var stage: Int
    var for_: Optional[String]
    var goal: Optional[String]
    var goal_metric: Optional[String]
    var goal_min: Optional[Float64]
    var goal_max: Optional[Float64]
    var last_touched: Instant
    var incubate_until: Optional[Instant]
    var touch: Int  # ticks
    var touch_in: List[Int]  # ticks per stage, indexed by stage code
    var stage_log: List[StageAt]
    var ships: List[TimedText]
    var uses: List[Instant]
    var home: Optional[String]
    var work: List[TimedText]
    var retired_why: Optional[String]

    def enter(mut self, stage: Int, at: Instant):
        if stage == self.stage:
            return
        self.stage = stage
        self.stage_log.append(StageAt(stage, at.copy()))

    def add_touch(mut self, ticks: Int):
        """Work is counted against the stage the idea is in when it is recorded.
        """
        self.touch += ticks
        self.touch_in[self.stage] = self.touch_in[self.stage] + ticks

    def entered(self, stage: Int) -> Bool:
        for entry in self.stage_log:
            if entry.stage == stage:
                return True
        return False

    def time_in(self, stage: Int, now: Instant) -> Int:
        """Ticks the idea has sat in a stage, working or waiting."""
        var total = 0
        var n = len(self.stage_log)
        for i in range(n):
            if self.stage_log[i].stage != stage:
                continue
            var until = now.utc if i + 1 >= n else self.stage_log[i + 1].at.utc
            total += until - self.stage_log[i].at.utc
        return total

    def goal_target(self) raises -> String:
        """The goal as text: a range, a floor, or a ceiling."""
        if self.goal_min is not None and self.goal_max is not None:
            return (
                num(self.goal_min.value()) + ".." + num(self.goal_max.value())
            )
        if self.goal_min is not None:
            return "≥ " + num(self.goal_min.value())
        if self.goal_max is not None:
            return "≤ " + num(self.goal_max.value())
        raise Error("journal: goal of " + self.id + " has no number")


@fieldwise_init
struct Requirement(Copyable, Movable):
    var id: String
    var idea_id: String
    var text: String
    var who: String
    var why: String
    var cut_why: Optional[String]

    def live(self) -> Bool:
        return self.cut_why is None


@fieldwise_init
struct Note(Copyable, Movable):
    """Where is the place inside the source: a page, a chapter, a timestamp."""

    var id: String
    var at: Instant
    var text: String
    var refs: List[String]
    var tags: List[String]
    var where_: Optional[String]


@fieldwise_init
struct Source(Copyable, Movable):
    var id: String
    var at: Instant
    var citation: String
    var url: Optional[String]


@fieldwise_init
struct Issue(Copyable, Movable):
    var id: String
    var at: Instant
    var text: String
    var idea_id: Optional[String]
    var stage: Optional[Int]
    var big: Bool
    var broke_id: Optional[String]
    var root_cause: Optional[String]
    var principle_id: Optional[String]


@fieldwise_init
struct Principle(Copyable, Movable):
    var id: String
    var at: Instant
    var text: String
    var issue_id: Optional[String]
    var stage: Optional[Int]


@fieldwise_init
struct Prediction(Copyable, Movable):
    """A claim with a confidence, written before the test. right stays None until
    the result is in."""

    var id: String
    var at: Instant
    var text: String
    var confidence: Float64
    var idea_id: Optional[String]
    var right: Optional[Bool]
    var outcome: Optional[String]


@fieldwise_init
struct Commitment(Copyable, Movable):
    var at: Instant
    var text: String
    var idea_id: Optional[String]
    var kept: Optional[Bool]


@fieldwise_init
struct Bookmark(Copyable, Movable):
    var id: String
    var at: Instant
    var title: String
    var url: String
    var why: Optional[String]
    var tags: List[String]


@fieldwise_init
struct MemoryRecord(Copyable, Movable):
    """A memory-kind line: remember, memory, read or exchange (docs/memory.md, "Journal kinds for memory (mark 2)").

    The fold keeps these as plain lists for recall to read. No existing command reads them.
    Fields follow the Event they came from: text, refs, value, who, why, url, where and tags.
    """

    var kind: String
    var id: String
    var at: Instant
    var text: String
    var refs: List[String]
    var value: Optional[Float64]
    var who: Optional[String]
    var why: Optional[String]
    var url: Optional[String]
    var where_: Optional[String]
    var tags: List[String]


@fieldwise_init
struct Metric(Copyable, Movable):
    var name: String
    var points: List[MetricPoint]
    var spec: Optional[Spec]


def empty_touch_in() -> List[Int]:
    var out = List[Int]()
    for _ in range(RETIRED + 1):
        out.append(0)
    return out^


def new_idea(id: String, title: String, born: Instant) -> Idea:
    var log = List[StageAt]()
    log.append(StageAt(SEED, born.copy()))
    return Idea(
        id,
        title,
        born.copy(),
        SEED,
        None,
        None,
        None,
        None,
        None,
        born.copy(),
        None,
        0,
        empty_touch_in(),
        log^,
        List[TimedText](),
        List[Instant](),
        None,
        List[TimedText](),
        None,
    )


struct State(Movable):
    var ideas: List[Idea]
    var requirements: List[Requirement]
    var notes: List[Note]
    var sources: List[Source]
    var issues: List[Issue]
    var principles: List[Principle]
    var metrics: List[Metric]
    var predictions: List[Prediction]
    var commitments: List[Commitment]
    var bookmarks: List[Bookmark]
    var watches: List[Watch]
    var watching_since: Optional[Instant]
    var synced_to: Optional[Instant]
    var commits: List[CommitKey]
    # Bookmarks ever made, kept or not, so an id is never used twice.
    var bookmarks_made: Int
    # Memory-kind lines in journal order (see MemoryRecord). Nothing reads them yet.
    var memory_records: List[MemoryRecord]

    def __init__(out self):
        self.ideas = List[Idea]()
        self.requirements = List[Requirement]()
        self.notes = List[Note]()
        self.sources = List[Source]()
        self.issues = List[Issue]()
        self.principles = List[Principle]()
        self.metrics = List[Metric]()
        self.predictions = List[Prediction]()
        self.commitments = List[Commitment]()
        self.bookmarks = List[Bookmark]()
        self.watches = List[Watch]()
        self.watching_since = None
        self.synced_to = None
        self.commits = List[CommitKey]()
        self.bookmarks_made = 0
        self.memory_records = List[MemoryRecord]()

    # ---- lookups: an index into the list, or -1 ----

    def idea_index(self, id: String) -> Int:
        for i in range(len(self.ideas)):
            if self.ideas[i].id == id:
                return i
        return -1

    def requirement_index(self, id: String) -> Int:
        for i in range(len(self.requirements)):
            if self.requirements[i].id == id:
                return i
        return -1

    def note_index(self, id: String) -> Int:
        for i in range(len(self.notes)):
            if self.notes[i].id == id:
                return i
        return -1

    def source_index(self, id: String) -> Int:
        for i in range(len(self.sources)):
            if self.sources[i].id == id:
                return i
        return -1

    def issue_index(self, id: String) -> Int:
        for i in range(len(self.issues)):
            if self.issues[i].id == id:
                return i
        return -1

    def principle_index(self, id: String) -> Int:
        for i in range(len(self.principles)):
            if self.principles[i].id == id:
                return i
        return -1

    def metric_index(self, name: String) -> Int:
        for i in range(len(self.metrics)):
            if self.metrics[i].name == name:
                return i
        return -1

    def prediction_index(self, id: String) -> Int:
        for i in range(len(self.predictions)):
            if self.predictions[i].id == id:
                return i
        return -1

    def open_commitment_index(self) -> Int:
        """The first commitment nobody has answered for yet, or -1."""
        for i in range(len(self.commitments)):
            if self.commitments[i].kept is None:
                return i
        return -1

    def require_idea(self, id: String) raises -> Int:
        var i = self.idea_index(id)
        if i < 0:
            raise Error(
                "journal: event names an idea that was never created: " + id
            )
        return i

    def require_requirement(self, id: String) raises -> Int:
        var i = self.requirement_index(id)
        if i < 0:
            raise Error(
                "journal: event names a requirement that does not exist: " + id
            )
        return i

    def require_note(self, id: String) raises -> Int:
        var i = self.note_index(id)
        if i < 0:
            raise Error(
                "journal: event names a note that does not exist: " + id
            )
        return i

    def require_issue(self, id: String) raises -> Int:
        var i = self.issue_index(id)
        if i < 0:
            raise Error(
                "journal: event names an issue that does not exist: " + id
            )
        return i

    def require_prediction(self, id: String) raises -> Int:
        var i = self.prediction_index(id)
        if i < 0:
            raise Error(
                "journal: event names a prediction that does not exist: " + id
            )
        return i

    def has(self, id: String) -> Bool:
        """Whether the id names an idea, note, source, issue, requirement,
        principle or prediction."""
        return (
            self.idea_index(id) >= 0
            or self.note_index(id) >= 0
            or self.source_index(id) >= 0
            or self.issue_index(id) >= 0
            or self.requirement_index(id) >= 0
            or self.principle_index(id) >= 0
            or self.prediction_index(id) >= 0
        )

    def next_id(self, prefix: String) raises -> String:
        """The next id with the prefix letter: i1, r2, n3 and so on."""
        var count: Int
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
            raise Error("journal: no id prefix " + prefix)
        return prefix + String(count + 1)

    def notes_about(self, id: String) -> List[Int]:
        """Indices of the notes that refer to id."""
        var out = List[Int]()
        for i in range(len(self.notes)):
            for r in self.notes[i].refs:
                if r == id:
                    out.append(i)
                    break
        return out^

    def has_commit(self, idea_id: String, hash: String) -> Bool:
        for c in self.commits:
            if c.idea_id == idea_id and c.hash == hash:
                return True
        return False

    def breaks(self, principle_id: String) -> Int:
        """How many issues broke this principle."""
        var count = 0
        for x in self.issues:
            if x.broke_id is not None and x.broke_id.value() == principle_id:
                count += 1
        return count

    def watched(self, idea_index: Int) -> Bool:
        """True when work on the idea is counted without anyone recording it."""
        if self.ideas[idea_index].home is not None:
            return True
        for w in self.watches:
            if w.idea_id == self.ideas[idea_index].id and not w.use:
                return True
        return False

    def all_watches(self) raises -> List[Watch]:
        """Every watch in force: the ones recorded, and one implied by each home.
        Watches on retired ideas are left out."""
        var out = List[Watch]()
        for w in self.watches:
            out.append(w.copy())
        for i in range(len(self.ideas)):
            if self.ideas[i].home is not None:
                out.append(
                    Watch(
                        self.ideas[i].id,
                        home_pattern(self.ideas[i].home.value()),
                        False,
                    )
                )
        var kept = List[Watch]()
        for w in out:
            var idea = self.require_idea(w.idea_id)
            if self.ideas[idea].stage != RETIRED:
                kept.append(w.copy())
        return kept^

    def goal_met(self, idea_index: Int) raises -> Optional[Bool]:
        """None when the goal's metric has never been measured."""
        var metric_name = self.ideas[idea_index].goal_metric
        if metric_name is None:
            return None
        var m = self.metric_index(metric_name.value())
        if m < 0 or len(self.metrics[m].points) == 0:
            return None
        var latest = (
            self.metrics[m].points[len(self.metrics[m].points) - 1].value
        )
        var lo_ok = True
        if self.ideas[idea_index].goal_min is not None:
            lo_ok = latest >= self.ideas[idea_index].goal_min.value()
        var hi_ok = True
        if self.ideas[idea_index].goal_max is not None:
            hi_ok = latest <= self.ideas[idea_index].goal_max.value()
        return lo_ok and hi_ok

    # ---- the fold ----

    def metric_or_new(mut self, name: String) -> Int:
        var i = self.metric_index(name)
        if i < 0:
            self.metrics.append(Metric(name, List[MetricPoint](), None))
            i = len(self.metrics) - 1
        return i

    def touch_idea_of(mut self, id: String, at: Instant):
        """Credit the idea a fact points at with its last touch. A requirement
        counts for its idea."""
        var i = self.idea_index(id)
        if i < 0:
            var r = self.requirement_index(id)
            if r >= 0:
                i = self.idea_index(self.requirements[r].idea_id)
        if i >= 0:
            self.ideas[i].last_touched = at.copy()

    def apply(mut self, e: Event) raises:
        var kind = e.kind
        var id = e.id.copy().or_else("")
        var text = e.text.copy().or_else("")
        var refs = e.refs.copy().or_else(List[String]())
        var at = e.at.copy()

        if kind == "idea":
            self.ideas.append(new_idea(id, text, at))
        elif kind == "for":
            var i = self.require_idea(id)
            self.ideas[i].for_ = e.text.copy()
        elif kind == "goal":
            var i = self.require_idea(id)
            self.ideas[i].goal = e.text.copy()
            self.ideas[i].goal_metric = e.metric.copy()
            self.ideas[i].goal_min = e.value.copy()
            self.ideas[i].goal_max = e.hi.copy()
        elif kind == "req":
            if len(refs) == 0:
                raise Error("journal: req has no idea")
            self.requirements.append(
                Requirement(
                    id,
                    refs[0].copy(),
                    text,
                    e.who.copy().or_else(""),
                    e.why.copy().or_else(""),
                    None,
                )
            )
        elif kind == "cut":
            var r = self.require_requirement(id)
            self.requirements[r].cut_why = e.text.copy()
        elif kind == "stage":
            var i = self.require_idea(id)
            self.ideas[i].enter(stage_code(text), at)
            if self.ideas[i].stage == INCUBATE:
                var days = Float64(1.0)
                if e.value is not None:
                    days = e.value.value()
                self.ideas[i].incubate_until = at.add_days(days)
        elif kind == "watch":
            if self.watching_since is None:
                self.watching_since = at.copy()
            self.watches.append(
                Watch(id, text, e.value.copy().or_else(0.0) == 1.0)
            )
        elif kind == "unwatch":
            var kept = List[Watch]()
            for w in self.watches:
                if not (
                    w.idea_id == id and equals_ignore_case(w.pattern, text)
                ):
                    kept.append(w.copy())
            self.watches = kept^
        elif kind == "bookmark":
            self.bookmarks_made += 1
            if e.url is None:
                raise Error("journal: bookmark has no url")
            self.bookmarks.append(
                Bookmark(
                    id,
                    at.copy(),
                    text,
                    e.url.value(),
                    e.why.copy(),
                    e.tags.copy().or_else(List[String]()),
                )
            )
        elif kind == "unbookmark":
            var kept = List[Bookmark]()
            for b in self.bookmarks:
                if b.id != id:
                    kept.append(b.copy())
            self.bookmarks = kept^
        elif kind == "home":
            if self.watching_since is None:
                self.watching_since = at.copy()
            var i = self.require_idea(id)
            self.ideas[i].home = e.text.copy()
        elif kind == "work":
            var i = self.require_idea(id)
            self.ideas[i].work.append(TimedText(at.copy(), text))
            # The same pair twice is kept once.
            var hash = e.where_.copy().or_else("")
            if not self.has_commit(id, hash):
                self.commits.append(CommitKey(id, hash))
        elif kind == "sync":
            self.synced_to = at.copy()
        elif kind == "spent":
            var i = self.require_idea(id)
            self.ideas[i].add_touch(minutes_to_ticks(required_value(e.value)))
        elif kind == "ship":
            var i = self.require_idea(id)
            self.ideas[i].ships.append(TimedText(at.copy(), text))
            self.ideas[i].enter(TEND, at)
        elif kind == "used":
            var i = self.require_idea(id)
            self.ideas[i].uses.append(at.copy())
        elif kind == "retire":
            var i = self.require_idea(id)
            self.ideas[i].enter(RETIRED, at)
            self.ideas[i].retired_why = e.text.copy()
        elif kind == "source":
            self.sources.append(Source(id, at.copy(), text, e.url.copy()))
        elif kind == "note":
            self.notes.append(
                Note(
                    id,
                    at.copy(),
                    text,
                    refs.copy(),
                    e.tags.copy().or_else(List[String]()),
                    e.where_.copy(),
                )
            )
        elif kind == "link":
            var n = self.require_note(id)
            for r in refs:
                self.notes[n].refs.append(r.copy())
        elif kind == "log":
            var m = self.metric_or_new(id)
            self.metrics[m].points.append(
                MetricPoint(at.copy(), required_value(e.value))
            )
        elif kind == "spec":
            var m = self.metric_or_new(id)
            if e.hi is None:
                raise Error("journal: spec has no high limit")
            self.metrics[m].spec = Spec(required_value(e.value), e.hi.value())
        elif kind == "issue":
            var about = first_with_prefix(refs, 105)  # i
            var about_stage: Optional[Int] = None
            if about is not None:
                var i = self.require_idea(about.value())
                about_stage = self.ideas[i].stage
            var big = e.value.copy().or_else(0.0) == 1.0
            var broke = first_with_prefix(refs, 112)  # p
            self.issues.append(
                Issue(
                    id,
                    at.copy(),
                    text,
                    about.copy(),
                    about_stage,
                    big,
                    broke^,
                    None,
                    None,
                )
            )
        elif kind == "diagnose":
            var x = self.require_issue(id)
            self.issues[x].root_cause = e.text.copy()
        elif kind == "principle":
            var issue_index = -1
            if len(refs) > 0:
                issue_index = self.issue_index(refs[0])
            var issue_id: Optional[String] = None
            var issue_stage: Optional[Int] = None
            if issue_index >= 0:
                issue_id = self.issues[issue_index].id.copy()
                issue_stage = self.issues[issue_index].stage.copy()
            self.principles.append(
                Principle(id, at.copy(), text, issue_id^, issue_stage^)
            )
            if issue_index >= 0:
                self.issues[issue_index].principle_id = Optional[String](id)
        elif kind == "predict":
            var about_idea: Optional[String] = None
            if len(refs) > 0:
                about_idea = Optional[String](refs[0])
            self.predictions.append(
                Prediction(
                    id,
                    at.copy(),
                    text,
                    required_value(e.value),
                    about_idea^,
                    None,
                    None,
                )
            )
        elif kind == "result":
            var p = self.require_prediction(id)
            self.predictions[p].right = Optional[Bool](
                e.value.copy().or_else(0.0) == 1.0
            )
            self.predictions[p].outcome = e.text.copy()
        elif kind == "commit":
            var about_idea: Optional[String] = None
            if len(refs) > 0:
                about_idea = Optional[String](refs[0])
            self.commitments.append(
                Commitment(at.copy(), text, about_idea^, None)
            )
        elif kind == "kept" or kind == "missed":
            var c = self.open_commitment_index()
            if c < 0:
                raise Error("journal: " + kind + " with no open commitment")
            self.commitments[c].kept = Optional[Bool](kind == "kept")
        elif kind == "review":
            var m = self.metric_or_new(FINDINGS_METRIC)
            self.metrics[m].points.append(
                MetricPoint(at.copy(), required_value(e.value))
            )
        elif (
            kind == "remember"
            or kind == "memory"
            or kind == "read"
            or kind == "exchange"
        ):
            self.memory_records.append(
                MemoryRecord(
                    kind,
                    id,
                    at.copy(),
                    text,
                    refs.copy(),
                    e.value.copy(),
                    e.who.copy(),
                    e.why.copy(),
                    e.url.copy(),
                    e.where_.copy(),
                    e.tags.copy().or_else(List[String]()),
                )
            )
        # "tend" changes nothing but when the idea was last touched.
        # Unknown kinds are skipped so an older build can read a newer journal.

        # Metric names are free text, so they must not be read as ids.
        if kind == "log" or kind == "spec":
            return
        if e.id is not None:
            self.touch_idea_of(id, at)
        for r in refs:
            self.touch_idea_of(r, at)


def required_value(value: Optional[Float64]) raises -> Float64:
    """A field the event must carry: a missing one is an error."""
    if value is None:
        raise Error("journal: event is missing its value")
    return value.value()


def state_from(events: List[Event]) raises -> State:
    """State.From: fold every event in journal order."""
    var state = State()
    for i in range(len(events)):
        state.apply(events[i])
    return state^
