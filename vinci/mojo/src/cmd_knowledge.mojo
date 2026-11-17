# The knowledge group (App.cs "Knowledge", "Inquiry", "Measurement"). Owns these
# `case` labels of Dispatch: bookmark, unbookmark, bookmarks, source, note, link, notes,
# tags, sources, show, predict, result, predictions, log, spec, stats.
#
# show is the dispatcher for every id: an idea id goes to show_idea in cmd_ideas,
# and the other ids (note, source, prediction) are written here.
#
# `args` is the list after the command name (C#'s `a`). Return True when the command
# was handled, False to let the next group try it.

from cmd_ideas import show_idea
from session import Session, not_ported


def dispatch_knowledge(
    mut s: Session, cmd: String, args: List[String]
) raises -> Bool:
    if cmd == "show":
        var id = s.arg(args, 0, "id")
        var i = s.state.idea_index(id)
        if i >= 0:
            show_idea(s, i)
            return True
        raise not_ported("show " + id)
    return False
