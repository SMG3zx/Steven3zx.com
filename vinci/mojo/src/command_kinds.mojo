# Every command the vinci binary dispatches, classified as writing the journal or not (K0b).
#
# This table is the one place that says what a command may do to the journal. The chat's
# `journal` tool reads it twice: a command that is not a journal command (route is True, or the
# word is unknown) is refused without running, and a command that writes is gated by the
# person's yes once untrusted text is in the conversation (chat_gate.mojo). The unit test
# (tests/test_command_kinds.mojo) reads the dispatch sources and fails when a command is missing
# here, or here but not in the sources.
#
# Columns: writes is True when the command appends journal lines (directly, or through the
# tracker that runs before every journal command). route is True for commands that vinci.mojo
# routes before the journal is read: they are not journal commands and the chat never runs them.
# The index, the feeds and the backup write files outside the journal; docs/write-paths.md lists
# those. The chat itself (`vinci` with no word) is classed as writing, because its tools write.


struct CommandKind(Copyable, Movable):
    """One command word, whether it writes the journal, and whether it is routed.
    """

    var name: String
    var writes: Bool
    var route: Bool

    def __init__(out self, name: String, writes: Bool, route: Bool):
        self.name = name
        self.writes = writes
        self.route = route


def command_table() -> List[CommandKind]:
    """The table. A journal command is dispatched by dispatch_ideas, dispatch_knowledge or
    dispatch_ops (vinci.mojo runs them in that order); a route is handled before the journal.
    """
    var t = List[CommandKind]()
    # cmd_ideas.mojo: dispatch_ideas
    t.append(CommandKind("idea", True, False))
    t.append(CommandKind("for", True, False))
    t.append(CommandKind("goal", True, False))
    t.append(CommandKind("req", True, False))
    t.append(CommandKind("cut", True, False))
    t.append(CommandKind("advance", True, False))
    t.append(CommandKind("adopt", True, False))
    t.append(CommandKind("home", True, False))
    t.append(CommandKind("watch", True, False))
    t.append(CommandKind("unwatch", True, False))
    t.append(CommandKind("watches", False, False))
    t.append(CommandKind("spent", True, False))
    t.append(CommandKind("ship", True, False))
    t.append(CommandKind("used", True, False))
    t.append(CommandKind("tend", True, False))
    t.append(CommandKind("retire", True, False))
    t.append(CommandKind("ideas", False, False))
    t.append(CommandKind("pipeline", False, False))
    t.append(CommandKind("next", False, False))
    # cmd_knowledge.mojo: dispatch_knowledge
    t.append(CommandKind("bookmark", True, False))
    t.append(CommandKind("unbookmark", True, False))
    t.append(CommandKind("bookmarks", False, False))
    t.append(CommandKind("source", True, False))
    t.append(CommandKind("note", True, False))
    t.append(CommandKind("link", True, False))
    t.append(CommandKind("notes", False, False))
    t.append(CommandKind("tags", False, False))
    t.append(CommandKind("sources", False, False))
    t.append(CommandKind("show", False, False))
    t.append(CommandKind("predict", True, False))
    t.append(CommandKind("result", True, False))
    t.append(CommandKind("predictions", False, False))
    t.append(CommandKind("log", True, False))
    t.append(CommandKind("spec", True, False))
    t.append(CommandKind("stats", False, False))
    # cmd_ops.mojo: dispatch_ops
    t.append(CommandKind("help", False, False))
    t.append(CommandKind("--help", False, False))
    t.append(CommandKind("-h", False, False))
    t.append(CommandKind("path", False, False))
    t.append(CommandKind("sync", True, False))
    t.append(CommandKind("issue", True, False))
    t.append(CommandKind("diagnose", True, False))
    t.append(CommandKind("principle", True, False))
    t.append(CommandKind("issues", False, False))
    t.append(CommandKind("principles", False, False))
    t.append(CommandKind("commit", True, False))
    t.append(CommandKind("kept", True, False))
    t.append(CommandKind("missed", True, False))
    t.append(CommandKind("review", True, False))
    # vinci.mojo: routed before the journal is read
    t.append(CommandKind("chat", True, True))
    t.append(CommandKind("serve", False, True))
    t.append(CommandKind("index", False, True))
    t.append(CommandKind("feed", False, True))
    t.append(CommandKind("recall", False, True))
    t.append(CommandKind("open", False, True))
    t.append(CommandKind("memory", False, True))
    t.append(CommandKind("backup", False, True))
    t.append(CommandKind("verify", False, True))
    return t^


def find_command(name: String) raises -> Int:
    """The index of name in command_table(), or -1 when it is not a command."""
    var t = command_table()
    for i in range(len(t)):
        if t[i].name == name:
            return i
    return -1


def command_writes_journal(name: String) raises -> Bool:
    """True when the command appends journal lines. An unknown word counts as a write, so
    the chat asks before it can do anything."""
    var i = find_command(name)
    if i < 0:
        return True
    return command_table()[i].writes


def chat_runs(name: String) raises -> Bool:
    """True when the chat's journal tool may run this word: a journal command in the table.
    Anything else (a route, or a word the table does not know) is refused without running.
    """
    var i = find_command(name)
    if i < 0:
        return False
    return not command_table()[i].route


def journal_command_words() raises -> List[String]:
    """The words the journal dispatch handles, in table order (the chat's instructions list
    them, and the tests compare them with the sources)."""
    var out = List[String]()
    for k in command_table():
        if not k.route:
            out.append(k.name.copy())
    return out^
