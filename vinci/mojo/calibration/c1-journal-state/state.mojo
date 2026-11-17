# The fold behind `vinci ideas`, ported from src/State.cs. Only what decides an
# idea's id, stage, touch and title is kept. Other kinds are read and ignored.

from journal import Event
from format import format_span, minutes_to_ticks, pad_right

comptime SEED = 0
comptime PREPARE = 1
comptime INCUBATE = 2
comptime BUILD = 3
comptime TEND = 4
comptime RETIRED = 5


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
    # Enum.Parse<Stage> is case-sensitive, so the match is exact.
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


@fieldwise_init
struct Idea(Copyable, Movable):
    var id: String
    var title: String
    var stage: Int
    var touch: Int  # ticks


struct State(Movable):
    var ideas: List[Idea]

    def __init__(out self):
        self.ideas = List[Idea]()

    def find(self, id: String) raises -> Int:
        for i in range(len(self.ideas)):
            if self.ideas[i].id == id:
                return i
        raise Error(
            "journal: event names an idea that was never created: " + id
        )

    def apply(mut self, event: Event) raises:
        var kind = event.kind
        if kind == "idea":
            self.ideas.append(Idea(event.id, event.text, SEED, 0))
        elif kind == "stage":
            var i = self.find(event.id)
            self.ideas[i].stage = stage_code(event.text)
        elif kind == "ship":
            # Shipping moves the idea to Tend (src/State.cs, case "ship").
            var i = self.find(event.id)
            self.ideas[i].stage = TEND
        elif kind == "retire":
            var i = self.find(event.id)
            self.ideas[i].stage = RETIRED
        elif kind == "spent":
            if not event.has_value:
                raise Error("journal: spent has no minutes")
            var i = self.find(event.id)
            self.ideas[i].touch += minutes_to_ticks(event.value)
        # Every other kind (tend, work, home, sync, log, ...) changes neither the
        # stage, the touch time nor the title, so it is skipped.


def idea_line(idea: Idea) -> String:
    return (
        pad_right(idea.id, 4)
        + " "
        + pad_right(stage_name(idea.stage), 8)
        + " touch "
        + pad_right(format_span(idea.touch), 7)
        + " "
        + idea.title
    )
