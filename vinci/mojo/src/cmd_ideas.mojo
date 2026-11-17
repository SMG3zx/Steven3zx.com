# The ideas group (App.cs "ideas: inception to grown"). Owns these `case` labels of
# Dispatch: idea, for, goal, req, cut, advance, adopt, home, watch, unwatch, watches,
# spent, ship, used, tend, retire, ideas, pipeline, next, and the idea part of show
# (show_idea, which cmd_knowledge's show calls).
#
# `args` is the list after the command name (C#'s `a`). Return True when the command
# was handled, False to let the next group try it.

from session import Session, not_ported


def dispatch_ideas(
    mut s: Session, cmd: String, args: List[String]
) raises -> Bool:
    return False


def show_idea(s: Session, idea_index: Int) raises:
    """ShowIdea: the idea's full view. Not written yet."""
    raise not_ported("show idea")
