# The citation check (Conversation.cs: Citation, and the unsourced check) for the Mojo chat.
# A citation is an id in square brackets, as recall prints it: a letter, digits, and for a
# read passage a slash and more digits ([i1], [m12], [L142], [d1/2]). Each cited id must
# appear in the tool results of the same turn, as "[id]". Anything else is reported.

comptime OPEN_BRACKET = 91
comptime CLOSE_BRACKET = 93
comptime MAX_CITE_BYTES = 24
comptime SLASH = 47


def is_digit(c: Int) -> Bool:
    return c >= 48 and c <= 57


def is_letter(c: Int) -> Bool:
    return (c >= 65 and c <= 90) or (c >= 97 and c <= 122)


def is_cite_id(text: String) -> Bool:
    """A letter, one or more digits, and an optional /digits: i1, L142, d1/2."""
    var b = text.as_bytes()
    var n = len(b)
    if n < 2 or not is_letter(Int(b[0])):
        return False
    var i = 1
    var digits = 0
    while i < n and is_digit(Int(b[i])):
        digits += 1
        i += 1
    if digits == 0:
        return False
    if i == n:
        return True
    if Int(b[i]) != SLASH:
        return False
    i += 1
    var more = 0
    while i < n and is_digit(Int(b[i])):
        more += 1
        i += 1
    return more > 0 and i == n


def cited_ids(answer: String) raises -> List[String]:
    """The distinct citation ids in an answer, in the order they first appear. A bracket
    pair with white space, a second bracket, or a long run between is not a citation.
    """
    var out = List[String]()
    var b = answer.as_bytes()
    var n = len(b)
    var i = 0
    while i < n:
        if Int(b[i]) != OPEN_BRACKET:
            i += 1
            continue
        var j = i + 1
        var clean = True
        while j < n and Int(b[j]) != CLOSE_BRACKET:
            var c = Int(b[j])
            if c == 32 or c == 9 or c == 10 or c == 13 or c == OPEN_BRACKET:
                clean = False
                break
            if j - i > MAX_CITE_BYTES:
                clean = False
                break
            j += 1
        if clean and j < n and Int(b[j]) == CLOSE_BRACKET and j > i + 1:
            var inside = List[UInt8]()
            for k in range(i + 1, j):
                inside.append(b[k])
            var id = String(unsafe_from_utf8=inside)
            if is_cite_id(id):
                var seen = False
                for old in out:
                    if old == id:
                        seen = True
                        break
                if not seen:
                    out.append(id)
            i = j + 1
        else:
            i += 1
    return out^


def unseen_cites(cited: List[String], evidence: String) raises -> List[String]:
    """The cited ids whose "[id]" does not appear in the evidence (the tool results of this
    turn). Conversation.cs: unsourced."""
    var out = List[String]()
    for id in cited:
        if evidence.find("[" + id + "]") < 0:
            out.append(id.copy())
    return out^


def document_base(id: String) raises -> String:
    """The document a passage belongs to: d1/2 is d1. Other ids are their own base.
    """
    var b = id.as_bytes()
    var cut = len(b)
    for i in range(len(b)):
        if Int(b[i]) == SLASH:
            cut = i
            break
    var out = List[UInt8]()
    for i in range(cut):
        out.append(b[i])
    return String(unsafe_from_utf8=out)


def is_document_cite(id: String) -> Bool:
    """A read document or one of its passages: d followed by digits, with an optional /digits.
    """
    return id.startswith("d") and is_cite_id(id)
