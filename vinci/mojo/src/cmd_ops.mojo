# The ops group: help, path, and the improvement commands (issue, diagnose, principle,
# issues, principles, commit, kept, missed, review). Owns these `case` labels of Dispatch:
# issue, diagnose, principle, issues, principles, commit, kept, missed, review, path,
# help, --help, -h, sync.
#
# sync is not in the Mojo port: it shows what the C# tracker just counted from window and
# git activity, and this port never tracks, so it says so rather than print nothing.
#
# The unknown-command branch is not here: C# Dispatch ends with it, so vinci.mojo does too.
#
# `args` is the list after the command name (C#'s `a`). Return True when the command
# was handled, False to let the next group try it.

from session import Session, not_ported

# Dispatch's Help text (App.cs), byte for byte. `vinci help` prints it with no extra newline.
comptime HELP = """vinci: one journal for ideas, knowledge, time and measurements.
Run it with no command, or as "vinci chat", to talk to the chat model, which does
everything below through its tools. "vinci pipeline" shows the stages and the one
thing to do next. Everything below also works as a plain command.

An idea grows through five stages, and each move has a gate:
  Seed -> Prepare      needs who it is for, and a goal tied to a number
  Prepare -> Incubate  needs a note, a questioned requirement, a prediction
  Incubate -> Build    needs the wait to be over, and fewer than 3 in Build
  Build -> Tend        happens when you ship
  Tend                 never ends: ship again, use it, tend it, or retire it

Ideas
  idea "title"                        capture an idea
  for <idea> "who"                    who will use it
  goal <idea> "goal" <metric> at-least <n> | at-most <n> | between <a> <b>
  req <idea> "text" "who" "why"       a requirement, who asked, the reason
  cut <req> "why"                     delete a requirement
  advance <idea> [days]               pass the next gate, or say why not (days: incubation, default 1)
  ship <idea> "what"                  record something shipped
  used <idea> ["how it went"]         record that you used what you shipped
  tend <idea> "what"                  record upkeep on a grown idea
  retire <idea> "why"                 end an idea on purpose
  pipeline   next   ideas   show <id>

Time
  adopt <folder>                      make an existing folder an idea, named after it
  home <idea> <folder>                where the idea lives: its editor windows and commits count as work
  watch <idea> "text" [use]           count time in windows whose app or title contains the text;
                                      with "use", such a window counts as using what you shipped
  unwatch <idea> "text"               stop counting it
  watches                             what is watched
  sync                                show what was just counted (every command counts it)
  spent <idea> <minutes>              add time spent away from the screen

Portal
  bookmark "title" <url> ["what it is for"] [+tag...]
  unbookmark <b>   bookmarks
  serve [port]                        the page of saved links, on localhost (default port 4000).
                                      It also shows each project's portal.json and any web page
                                      this machine is serving.

Knowledge
  source "citation" [url]             where knowledge came from
  note "text" [id...] [+tag...]       one thought. s1:p.42 cites a place in a source
  link <note> <id>                    connect a note to a note, idea or prediction
  notes [word | +tag]   tags   sources

Inquiry
  predict "claim" <percent> [idea]    what you expect, before you test it
  result <h> right|wrong "what happened"
  predictions                         your record against your confidence

Measurement
  log <metric> <value> ["comment"]    measure anything
  spec <metric> <low> <high>          what counts as good
  stats [metric]                      control limits, and defects against a spec

Improvement
  issue "what went wrong" [idea] [big] [principle that failed]
  diagnose <issue> "root cause"       why it really happened
  principle "rule" [issue]            what you do from now on
  issues   principles
  commit "one thing" [idea]           what you will get done this week
  kept | missed "why"                 answer for it
  review                              what needs fixing now. Run it weekly.

In a pipe
  Any quoted text can be "-" to read it from standard input:
    git log -1 --format=%s | vinci ship i1 -
  vinci path prints the journal file, one JSON object per line:
    jq -r 'select(.kind=="ship") | .text' "$(vinci path)"

Data lives in $VINCI_HOME/journal.jsonl (default ~/.vinci).
Window time comes from ActivityWatch at $VINCI_ACTIVITYWATCH (default http://127.0.0.1:5600).
"""


def dispatch_ops(
    mut s: Session, cmd: String, args: List[String]
) raises -> Bool:
    if cmd == "help" or cmd == "--help" or cmd == "-h":
        print(HELP, end="")
        return True
    if cmd == "path":
        print(s.journal.path)
        return True
    if cmd == "sync":
        raise not_ported("sync")
    return False
