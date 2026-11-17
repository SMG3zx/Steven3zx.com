# `vinci ideas` without the age and flow columns, which depend on the clock.
# Usage: ideas <journal.jsonl>

from std.sys import argv

from journal import read_events
from state import State, idea_line


def main() raises:
    var args = argv()
    if len(args) < 2:
        raise Error("usage: ideas <journal.jsonl>")
    var events = read_events(String(args[1]))
    var state = State()
    for event in events:
        state.apply(event)
    if len(state.ideas) == 0:
        print('No ideas yet. vinci idea "title"')
        return
    for idea in state.ideas:
        print(idea_line(idea))
