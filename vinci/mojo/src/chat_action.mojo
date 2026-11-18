# A chat reply that asks for a tool (Conversation.TryAction in src/Chat/Conversation.cs).
# A reply that is one JSON object with a "tool" name is an action; anything else is the
# answer. The arguments are kept in order, with each value as a string, a list of strings,
# or other JSON text, so the summary shown to the person reads as the C# one does.

from jsonline import Cursor, ascii_lower, json_string

comptime VALUE_STRING = 0
comptime VALUE_LIST = 1
comptime VALUE_OTHER = 2
comptime SUMMARY_CHARS = 90
comptime BACKTICK = 96
comptime OPEN_BRACE = 123
comptime CLOSE_BRACE = 125
comptime QUOTE = 34


@fieldwise_init
struct ArgValue(Copyable, Movable):
    """One argument. text is the string (VALUE_STRING) or the raw JSON (VALUE_OTHER).
    items holds the strings of a VALUE_LIST."""

    var key: String
    var kind: Int
    var text: String
    var items: List[String]


@fieldwise_init
struct Action(Copyable, Movable):
    """The tool name and the arguments, without the "tool" key itself."""

    var name: String
    var values: List[ArgValue]


def is_ascii_space(c: Int) -> Bool:
    return c == 32 or c == 9 or c == 10 or c == 13


def trim_bytes(text: String, strip_backticks: Bool) -> String:
    """Removes white space, and backticks when asked, from both ends."""
    var b = List[UInt8]()
    for x in text.as_bytes():
        b.append(x)
    var start = 0
    var end = len(b)
    while start < end and (
        is_ascii_space(Int(b[start]))
        or (strip_backticks and Int(b[start]) == BACKTICK)
    ):
        start += 1
    while end > start and (
        is_ascii_space(Int(b[end - 1]))
        or (strip_backticks and Int(b[end - 1]) == BACKTICK)
    ):
        end -= 1
    var out = List[UInt8]()
    for i in range(start, end):
        out.append(b[i])
    return String(unsafe_from_utf8=out)


def strip_fence(reply: String) -> String:
    """A reply in a code fence: the backticks go, and a json tag after them. As TryAction.
    """
    var text = trim_bytes(reply, False)
    if not text.startswith("```"):
        return text
    text = trim_bytes(text, True)
    if ascii_lower(text).startswith("json"):
        var rest = List[UInt8]()
        var b = text.as_bytes()
        for i in range(4, len(b)):
            rest.append(b[i])
        text = trim_bytes(String(unsafe_from_utf8=rest), False)
    return text


def raw_slice(c: Cursor, start: Int, end: Int) -> String:
    var out = List[UInt8]()
    for i in range(start, end):
        out.append(c.data[i])
    return String(unsafe_from_utf8=out)


def parse_members(mut c: Cursor, mut values: List[ArgValue]) raises:
    """The members of one JSON object, after its opening brace, up to and including the
    closing brace."""
    while True:
        c.skip_space()
        if c.peek() == CLOSE_BRACE:
            _ = c.take()
            return
        var key = c.read_string()
        c.skip_space()
        c.expect(58)  # :
        c.skip_space()
        if c.peek() == QUOTE:
            values.append(
                ArgValue(key, VALUE_STRING, c.read_string(), List[String]())
            )
        elif c.peek() == 91:  # [
            values.append(
                ArgValue(key, VALUE_LIST, String(), c.read_string_array())
            )
        else:
            var start = c.pos
            c.skip_value()
            values.append(
                ArgValue(
                    key, VALUE_OTHER, raw_slice(c, start, c.pos), List[String]()
                )
            )
        c.skip_space()
        var sep = c.take()
        if sep == CLOSE_BRACE:
            return
        if sep != 44:  # ,
            raise Error("chat: expected , or } in an action")


def parse_action(reply: String) raises -> Optional[Action]:
    """The action a reply asks for, or None when the reply is the answer. A reply that
    starts with { but is not one JSON object, or has no string "tool", is an answer.
    """
    var text = strip_fence(reply)
    if not text.startswith("{"):
        return None
    var values = List[ArgValue]()
    try:
        var c = Cursor(text)
        c.skip_space()
        c.expect(OPEN_BRACE)
        parse_members(c, values)
        c.skip_space()
        if not c.at_end():
            return None
    except:
        return None
    var name_at = -1
    for i in range(len(values)):
        if values[i].key == "tool" and values[i].kind == VALUE_STRING:
            name_at = i
            break
    if name_at < 0:
        return None
    var name = values[name_at].text.copy()
    var rest = List[ArgValue]()
    for i in range(len(values)):
        if i != name_at:
            rest.append(values[i].copy())
    return Optional[Action](Action(name, rest^))


def find_value(a: Action, key: String) -> Int:
    for i in range(len(a.values)):
        if a.values[i].key == key:
            return i
    return -1


def has_arg(a: Action, key: String) -> Bool:
    return find_value(a, key) >= 0


def text_arg(a: Action, key: String) raises -> String:
    """A string argument. Missing is an error that names the key; a non-string is one too.
    """
    var at = find_value(a, key)
    if at < 0:
        raise Error(key + " is required")
    if a.values[at].kind != VALUE_STRING:
        raise Error(key + " must be text")
    return a.values[at].text.copy()


def optional_text_arg(a: Action, key: String) raises -> Optional[String]:
    if not has_arg(a, key):
        return None
    return Optional[String](text_arg(a, key))


def list_arg(a: Action, key: String) raises -> List[String]:
    """A list of strings. Missing or not a list is an error that names the form.
    """
    var at = find_value(a, key)
    if at < 0 or a.values[at].kind != VALUE_LIST:
        raise Error(key + " must be a list of strings")
    return a.values[at].items.copy()


def int_arg(a: Action, key: String, fallback: Int) raises -> Int:
    """An optional whole number. A string of digits is read; anything else is an error.
    """
    var at = find_value(a, key)
    if at < 0:
        return fallback
    if a.values[at].kind == VALUE_LIST:
        raise Error(key + " must be a whole number")
    var text = a.values[at].text.copy()
    try:
        return Int(text)
    except:
        raise Error(key + " must be a whole number")


def compact_json(a: Action) raises -> String:
    """The arguments as one line of JSON, the way the C# Summary writes them: keys in
    order, no spaces, the tool name left out."""
    var out = String("{")
    for i in range(len(a.values)):
        if i > 0:
            out += ","
        var v = a.values[i].copy()
        out += json_string(v.key) + ":"
        if v.kind == VALUE_STRING:
            out += json_string(v.text)
        elif v.kind == VALUE_LIST:
            out += "["
            for j in range(len(v.items)):
                if j > 0:
                    out += ","
                out += json_string(v.items[j])
            out += "]"
        else:
            out += v.text
    return out + "}"


def summary_of(a: Action) raises -> String:
    """Summary: the arguments as JSON, cut to 90 characters with an ellipsis."""
    var text = compact_json(a)
    if text.byte_length() <= SUMMARY_CHARS:
        return text
    var kept = List[UInt8]()
    var b = text.as_bytes()
    for i in range(SUMMARY_CHARS - 1):
        kept.append(b[i])
    return String(unsafe_from_utf8=kept) + "…"
