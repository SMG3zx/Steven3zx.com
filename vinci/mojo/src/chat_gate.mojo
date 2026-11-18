# The write confirmation (M4 plan, section 3: the prompt injection row).
#
# Once untrusted text has entered the chat's context (the read tool saved a document, or a
# recall or open returned a passage that is not the person's own journal line), a tool call
# that writes is printed and needs a yes from the person on standard input before it runs.
# Anything but y or yes is a no, and end of input is a no. A no reaches the model as the tool
# result, so it can say that nothing was written. Journal-only turns write without asking.

from chat_edge import read_line
from command_kinds import chat_runs, command_writes_journal
from jsonline import ascii_lower

comptime REFUSAL = (
    "refused: the person did not allow this write, so nothing was written."
    " Say so, and do not retry it unless the person asks."
)


def is_read_only_command(command: String) raises -> Bool:
    """True for a journal command that only reads, as command_kinds.mojo classes it. Anything
    else is a write: sync (the tracker records time in normal use), review (it appends a review
    line), and any word the table does not know.
    """
    return chat_runs(command) and not command_writes_journal(command)


def is_write_call(name: String, args: List[String]) raises -> Bool:
    """True when the tool call writes. journal writes unless its command only reads; remember
    and read write. recall and open only read. An unknown name is not a write: the tool
    result says there is no such tool."""
    if name == "journal":
        var command = String("help")
        if len(args) > 0:
            command = args[0].copy()
        return not is_read_only_command(command)
    return name == "remember" or name == "read"


def needs_confirmation(untrusted: Bool, write: Bool) -> Bool:
    """The gate: a write after untrusted text needs a yes."""
    return untrusted and write


def is_yes(answer: String) -> Bool:
    """y or yes, in any case, with white space around it. Anything else is a no.
    """
    var word = ascii_lower(String(answer.strip()))
    return word == "y" or word == "yes"


def ask_write(description: String) raises -> Bool:
    """Prints the write that is waiting, and reads the person's answer. True only for a yes.
    End of input is a no, so a piped run without answers writes nothing."""
    print(
        "  ? "
        + description
        + " This is a write, and untrusted text is in this"
        " conversation. Allow it? [y/N] ",
        end="",
    )
    var line = read_line()
    if line is None:
        print("")
        return False
    return is_yes(line.value())


def refusal() -> String:
    return REFUSAL
