# The vaults feed: Markdown files under the configured folders, one record per heading section
# (M5 step a). The sections are cut as atlas cuts them: a section runs from its heading to the
# next heading of any level, so no text is in two records.
#
# Record: id is "<folder>/<file>.md#<slug>", where the path is from the root's parent folder
# and the slug is the heading's text in lower-case ASCII with dashes. A repeated slug in one
# file gets "-2", "-3" ... in document order. The preamble (text before the first heading) is
# "top". title is the breadcrumb: the file's title (its first level-1 heading, else the file
# name), then the headings above this one and this one. text is the breadcrumb, a blank line,
# and the section's lines, headings and code fences included. links are the id of the enclosing
# section. body_path is the file. Headings inside fenced code do not start a section.
#
# Cursor: one line per file, tab-separated: relative path, mtime (ns), size, sha-256 of the text,
# and the ids of its sections joined by "|". A file whose mtime and size match is not read. A
# file that changed but has the same text hash keeps its records. A file missing from the walk,
# or one whose sections changed, leaves its old ids out of the new set, and those records are
# retracted.

from std.python import PythonObject

from feed_edge import list_markdown, read_text
from feed_store import FeedRead, FeedRecord, VAULT_SOURCE, feed_record
from index_store import sha256_hex
from open_view import utf8_prefix
from text import ascii_tail

comptime HASH = 35  # '#'
comptime SPACE = 32
comptime TAB = 9
comptime DASH = 45
comptime MAX_LEVEL = 6
comptime ID_SEPARATOR = "|"


def drop_cr(line: String) raises -> String:
    """The line without a trailing carriage return."""
    var n = line.byte_length()
    if n > 0 and Int(line.as_bytes()[n - 1]) == 13:
        return utf8_prefix(line, n - 1)
    return line.copy()


def heading_level(line: String) -> Int:
    """1 to 6 for an ATX heading at the start of the line ("## Text"), else 0. A '#' that is
    not followed by a space or the end of the line is not a heading."""
    var b = line.as_bytes()
    var n = 0
    while n < len(b) and Int(b[n]) == HASH:
        n += 1
    if n == 0 or n > MAX_LEVEL:
        return 0
    if n < len(b) and Int(b[n]) != SPACE and Int(b[n]) != TAB:
        return 0
    return n


def heading_text(line: String, level: Int) raises -> String:
    """The text of an ATX heading, without its hashes and surrounding space."""
    return String(ascii_tail(line, level).strip())


def is_fence(line: String) raises -> Bool:
    var t = String(line.strip())
    return t.startswith("```") or t.startswith("~~~")


def slugify(title: String) raises -> String:
    """Lower-case ASCII letters and digits, with runs of space, dash or underscore as one
    dash. Other bytes are dropped, so an accented letter is dropped, not transliterated.
    """
    var out = List[UInt8]()
    for b in title.as_bytes():
        var c = Int(b)
        if c >= 65 and c <= 90:
            out.append(UInt8(c + 32))
        elif (c >= 97 and c <= 122) or (c >= 48 and c <= 57):
            out.append(b)
        elif c == SPACE or c == DASH or c == 95 or c == TAB:
            if len(out) > 0 and Int(out[len(out) - 1]) != DASH:
                out.append(UInt8(DASH))
    while len(out) > 0 and Int(out[len(out) - 1]) == DASH:
        _ = out.pop()
    if len(out) == 0:
        return "section"
    return String(unsafe_from_utf8=out)


def unique_slug(base: String, mut used: Dict[String, Bool]) raises -> String:
    """base, or base-2, base-3 ..., whichever no earlier section of the file has taken.
    """
    var candidate = base
    var n = 2
    while candidate in used:
        candidate = base + "-" + String(n)
        n += 1
    used[candidate] = True
    return candidate


def file_stem(rel: String) raises -> String:
    """The file name without its folder and its .md or .markdown extension."""
    var b = rel.as_bytes()
    var start = 0
    for i in range(len(b)):
        if Int(b[i]) == 47:
            start = i + 1
    var name = String(ascii_tail(rel, start))
    if name.endswith(".markdown"):
        return utf8_prefix(name, name.byte_length() - 9)
    if name.endswith(".md"):
        return utf8_prefix(name, name.byte_length() - 3)
    return name


def join_lines(lines: List[String], start: Int, end: Int) raises -> String:
    """Lines start to end (exclusive) joined by newlines, without trailing blank lines.
    """
    var last = end
    while last > start and String(lines[last - 1].strip()).byte_length() == 0:
        last -= 1
    var out = String()
    for i in range(start, last):
        if i > start:
            out += "\n"
        out += lines[i]
    return out^


def has_text(lines: List[String], start: Int, end: Int) raises -> Bool:
    for i in range(start, end):
        if String(lines[i].strip()).byte_length() > 0:
            return True
    return False


def section_record(
    rel: String,
    slug: String,
    at: String,
    path: String,
    crumb: String,
    body: String,
    parent: String,
) raises -> FeedRecord:
    var id = rel + "#" + slug
    var links = List[String]()
    if parent.byte_length() > 0:
        links.append(parent)
    return feed_record(
        VAULT_SOURCE,
        id,
        at,
        "section",
        crumb,
        path,
        crumb + "\n\n" + body,
        links,
    )


def sections_of(
    rel: String, path: String, at: String, text: String
) raises -> List[FeedRecord]:
    """The sections of one file, in document order. See the header for the ids and text.
    """
    var lines = List[String]()
    for raw in text.split("\n"):
        lines.append(drop_cr(String(raw)))

    var heads = List[Int]()
    var levels = List[Int]()
    var titles = List[String]()
    var in_fence = False
    for i in range(len(lines)):
        if is_fence(lines[i]):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        var level = heading_level(lines[i])
        if level > 0:
            heads.append(i)
            levels.append(level)
            titles.append(heading_text(lines[i], level))

    # The file's title: its first level-1 heading, else its name. That heading is not
    # pushed on the breadcrumb stack, since the title already starts every breadcrumb.
    var title_at = -1
    for k in range(len(heads)):
        if levels[k] == 1 and titles[k].byte_length() > 0:
            title_at = k
            break
    var file_title = file_stem(rel)
    if title_at >= 0:
        file_title = titles[title_at]

    var used = Dict[String, Bool]()
    var out = List[FeedRecord]()
    var first = len(lines)
    if len(heads) > 0:
        first = heads[0]
    if first > 0 and has_text(lines, 0, first):
        var slug = unique_slug("top", used)
        out.append(
            section_record(
                rel,
                slug,
                at,
                path,
                file_title,
                join_lines(lines, 0, first),
                String(),
            )
        )

    # The stack holds each enclosing heading, the title included, so a section's parent is
    # the nearest heading above it. The title is left out of the breadcrumb: crumb starts
    # with it already.
    var stack_levels = List[Int]()
    var stack_titles = List[String]()
    var stack_ids = List[String]()
    var stack_is_title = List[Bool]()
    for k in range(len(heads)):
        var start = heads[k]
        var end = len(lines)
        if k + 1 < len(heads):
            end = heads[k + 1]
        var slug = unique_slug(slugify(titles[k]), used)
        var self_id = rel + "#" + slug
        while (
            len(stack_levels) > 0
            and stack_levels[len(stack_levels) - 1] >= levels[k]
        ):
            _ = stack_levels.pop()
            _ = stack_titles.pop()
            _ = stack_ids.pop()
            _ = stack_is_title.pop()
        var parent = String()
        if len(stack_ids) > 0:
            parent = stack_ids[len(stack_ids) - 1]
        var crumb = file_title
        if k != title_at:
            for j in range(len(stack_titles)):
                if not stack_is_title[j]:
                    crumb += " > " + stack_titles[j]
            crumb += " > " + titles[k]
        out.append(
            section_record(
                rel,
                slug,
                at,
                path,
                crumb,
                join_lines(lines, start, end),
                parent,
            )
        )
        stack_levels.append(levels[k])
        stack_titles.append(titles[k])
        stack_ids.append(self_id)
        stack_is_title.append(k == title_at)
    return out^


def extend_all(mut dst: List[String], src: List[String]):
    for item in src:
        dst.append(item.copy())


def split_fields(line: String) raises -> List[String]:
    var out = List[String]()
    for raw in line.split("\t"):
        out.append(String(raw))
    return out^


def ids_of(field: String) raises -> List[String]:
    """The section ids in a cursor line's last field."""
    var out = List[String]()
    for raw in field.split(ID_SEPARATOR):
        if String(raw).byte_length() > 0:
            out.append(String(raw))
    return out^


def cursor_line(
    rel: String, mtime: String, size: String, digest: String, ids: List[String]
) raises -> String:
    var joined = String()
    for i in range(len(ids)):
        if i > 0:
            joined += ID_SEPARATOR
        joined += ids[i]
    return rel + "\t" + mtime + "\t" + size + "\t" + digest + "\t" + joined


def parse_cursor(cursor: String) raises -> Dict[String, String]:
    """The cursor's lines by relative path. A malformed line is ignored, so its file is read
    again."""
    var out = Dict[String, String]()
    for raw in cursor.split("\n"):
        var line = String(raw)
        if line.byte_length() == 0:
            continue
        var fields = split_fields(line)
        if len(fields) == 5:
            out[fields[0]] = line^
    return out^


def read_vaults(
    roots: List[String],
    exclude: List[String],
    max_bytes: Int,
    cursor: String,
) raises -> FeedRead:
    """The sections of every file that changed since the cursor, and the ids of every
    section the vaults have now. Raises when a folder cannot be walked."""
    var listing = list_markdown(roots, exclude, max_bytes)
    if listing.status != "ok":
        raise Error("vaults: " + String(listing.value))
    var previous = parse_cursor(cursor)
    var files = listing.value["files"]
    var skipped = listing.value["skipped"]
    var skipped_count = 0
    for _ in skipped:
        skipped_count += 1
    var walked = 0

    var records = List[FeedRecord]()
    var all_ids = List[String]()
    var lines_out = List[String]()
    var changed = 0
    var unchanged = 0
    var unreadable = 0
    for f in files:
        walked += 1
        var rel = String(f["rel"])
        var mtime = String(f["mtime_ns"])
        var size = String(f["size"])
        var at = String(f["iso"])
        var path = String(f["path"])
        var known = rel in previous
        if known:
            var old = split_fields(previous[rel])
            if String(old[1]) == mtime and String(old[2]) == size:
                lines_out.append(previous[rel])
                extend_all(all_ids, ids_of(String(old[4])))
                unchanged += 1
                continue

        var reply = read_text(path)
        if reply.status != "ok":
            unreadable += 1
            if known:
                lines_out.append(previous[rel])
                extend_all(
                    all_ids, ids_of(String(split_fields(previous[rel])[4]))
                )
            continue
        var text = String(reply.value)
        var digest = sha256_hex(text)
        if known:
            var old = split_fields(previous[rel])
            if String(old[3]) == digest:
                var ids = ids_of(String(old[4]))
                lines_out.append(cursor_line(rel, mtime, size, digest, ids))
                extend_all(all_ids, ids)
                unchanged += 1
                continue

        var sections = sections_of(rel, path, at, text)
        var ids = List[String]()
        for s in sections:
            ids.append(s.id)
            records.append(s.copy())
        extend_all(all_ids, ids)
        lines_out.append(cursor_line(rel, mtime, size, digest, ids))
        changed += 1

    var body = String()
    for i in range(len(lines_out)):
        if i > 0:
            body += "\n"
        body += lines_out[i]
    var note = (
        String(walked)
        + " files: "
        + String(changed)
        + " read, "
        + String(unchanged)
        + " unchanged, "
        + String(skipped_count)
        + " skipped (size, name or symlink), "
        + String(unreadable)
        + " unreadable"
    )
    var seen = len(all_ids)
    return FeedRead(
        records^,
        all_ids^,
        True,
        body,
        seen,
        note,
    )
