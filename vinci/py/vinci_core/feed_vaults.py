"""The vaults feed: Markdown files under the configured folders, one record per heading section.

The sections are cut as atlas cuts them: a section runs from its heading to the next heading of
any level, so no text is in two records.

Record: id is "<folder>/<file>.md#<slug>", where the path is from the root's parent folder and
the slug is the heading's text in lower-case ASCII with dashes. A repeated slug in one file gets
"-2", "-3" ... in document order. The preamble (text before the first heading) is "top". title is
the breadcrumb: the file's title (its first level-1 heading, else the file name), then the headings
above this one and this one. text is the breadcrumb, a blank line, and the section's lines,
headings and code fences included. links are the id of the enclosing section. body_path is the
file. Headings inside fenced code do not start a section.

Cursor: one line per file, tab-separated: relative path, mtime (ns), size, sha-256 of the text,
and the ids of its sections joined by "|". A file whose mtime and size match is not read. A file
that changed but has the same text hash keeps its records. A file missing from the walk, or one
whose sections changed, leaves its old ids out of the new set, and those records are retracted.

Port of mojo/src/feed_vaults.mojo.
"""

from __future__ import annotations

from vinci_edge import feed_edge

from .feed_store import VAULT_SOURCE, FeedRead, FeedRecord, feed_record
from .index_store import sha256_hex

MAX_LEVEL = 6
ID_SEPARATOR = "|"


def drop_cr(line: str) -> str:
    """The line without a trailing carriage return."""
    if line.endswith("\r"):
        return line[:-1]
    return line


def heading_level(line: str) -> int:
    """1 to 6 for an ATX heading at the start of the line ("## Text"), else 0. A '#' that is not
    followed by a space or the end of the line is not a heading."""
    n = 0
    while n < len(line) and line[n] == "#":
        n += 1
    if n == 0 or n > MAX_LEVEL:
        return 0
    if n < len(line) and line[n] not in (" ", "\t"):
        return 0
    return n


def heading_text(line: str, level: int) -> str:
    """The text of an ATX heading, without its hashes and surrounding space."""
    return line[level:].strip()


def is_fence(line: str) -> bool:
    t = line.strip()
    return t.startswith("```") or t.startswith("~~~")


def slugify(title: str) -> str:
    """Lower-case ASCII letters and digits, with runs of space, dash or underscore as one dash.
    Other bytes are dropped, so an accented letter is dropped, not transliterated."""
    out: list[str] = []
    for c in title.encode("utf-8"):
        if 65 <= c <= 90:
            out.append(chr(c + 32))
        elif (97 <= c <= 122) or (48 <= c <= 57):
            out.append(chr(c))
        elif c in (32, 45, 95, 9):
            if len(out) > 0 and out[-1] != "-":
                out.append("-")
    while len(out) > 0 and out[-1] == "-":
        out.pop()
    if len(out) == 0:
        return "section"
    return "".join(out)


def unique_slug(base: str, used: set[str]) -> str:
    """base, or base-2, base-3 ..., whichever no earlier section of the file has taken."""
    candidate = base
    n = 2
    while candidate in used:
        candidate = base + "-" + str(n)
        n += 1
    used.add(candidate)
    return candidate


def file_stem(rel: str) -> str:
    """The file name without its folder and its .md or .markdown extension."""
    name = rel[rel.rfind("/") + 1 :]
    if name.endswith(".markdown"):
        return name[: -len(".markdown")]
    if name.endswith(".md"):
        return name[: -len(".md")]
    return name


def join_lines(lines: list[str], start: int, end: int) -> str:
    """Lines start to end (exclusive) joined by newlines, without trailing blank lines."""
    last = end
    while last > start and lines[last - 1].strip() == "":
        last -= 1
    return "\n".join(lines[start:last])


def has_text(lines: list[str], start: int, end: int) -> bool:
    return any(lines[i].strip() != "" for i in range(start, end))


def section_record(rel: str, slug: str, at: str, path: str, crumb: str, body: str, parent: str) -> FeedRecord:
    ident = rel + "#" + slug
    links = [parent] if parent != "" else []
    return feed_record(VAULT_SOURCE, ident, at, "section", crumb, path, crumb + "\n\n" + body, links)


def sections_of(rel: str, path: str, at: str, text: str) -> list[FeedRecord]:
    """The sections of one file, in document order. See the header for the ids and text."""
    lines = [drop_cr(raw) for raw in text.split("\n")]

    heads: list[int] = []
    levels: list[int] = []
    titles: list[str] = []
    in_fence = False
    for i, line in enumerate(lines):
        if is_fence(line):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        level = heading_level(line)
        if level > 0:
            heads.append(i)
            levels.append(level)
            titles.append(heading_text(line, level))

    # The file's title: its first level-1 heading, else its name. That heading is not pushed on
    # the breadcrumb stack, since the title already starts every breadcrumb.
    title_at = -1
    for k in range(len(heads)):
        if levels[k] == 1 and len(titles[k]) > 0:
            title_at = k
            break
    file_title = file_stem(rel)
    if title_at >= 0:
        file_title = titles[title_at]

    used: set[str] = set()
    out: list[FeedRecord] = []
    first = heads[0] if len(heads) > 0 else len(lines)
    if first > 0 and has_text(lines, 0, first):
        slug = unique_slug("top", used)
        out.append(section_record(rel, slug, at, path, file_title, join_lines(lines, 0, first), ""))

    # The stack holds each enclosing heading, the title included, so a section's parent is the
    # nearest heading above it. The title is left out of the breadcrumb: crumb starts with it
    # already.
    stack_levels: list[int] = []
    stack_titles: list[str] = []
    stack_ids: list[str] = []
    stack_is_title: list[bool] = []
    for k in range(len(heads)):
        start = heads[k]
        end = heads[k + 1] if k + 1 < len(heads) else len(lines)
        slug = unique_slug(slugify(titles[k]), used)
        self_id = rel + "#" + slug
        while len(stack_levels) > 0 and stack_levels[-1] >= levels[k]:
            stack_levels.pop()
            stack_titles.pop()
            stack_ids.pop()
            stack_is_title.pop()
        parent = stack_ids[-1] if len(stack_ids) > 0 else ""
        crumb = file_title
        if k != title_at:
            for j in range(len(stack_titles)):
                if not stack_is_title[j]:
                    crumb += " > " + stack_titles[j]
            crumb += " > " + titles[k]
        out.append(section_record(rel, slug, at, path, crumb, join_lines(lines, start, end), parent))
        stack_levels.append(levels[k])
        stack_titles.append(titles[k])
        stack_ids.append(self_id)
        stack_is_title.append(k == title_at)
    return out


def ids_of(field: str) -> list[str]:
    """The section ids in a cursor line's last field."""
    return [raw for raw in field.split(ID_SEPARATOR) if len(raw) > 0]


def cursor_line(rel: str, mtime: str, size: str, digest: str, ids: list[str]) -> str:
    return rel + "\t" + mtime + "\t" + size + "\t" + digest + "\t" + ID_SEPARATOR.join(ids)


def parse_cursor(cursor: str) -> dict[str, str]:
    """The cursor's lines by relative path. A malformed line is ignored, so its file is read
    again."""
    out: dict[str, str] = {}
    for raw in cursor.split("\n"):
        if len(raw) == 0:
            continue
        fields = raw.split("\t")
        if len(fields) == 5:
            out[fields[0]] = raw
    return out


def read_vaults(roots: list[str], exclude: list[str], max_bytes: int, cursor: str) -> FeedRead:
    """The sections of every file that changed since the cursor, and the ids of every section
    the vaults have now. Raises when a folder cannot be walked."""
    status, listing = feed_edge.list_markdown(roots, exclude, max_bytes)
    if status != "ok":
        raise ValueError("vaults: " + str(listing))
    previous = parse_cursor(cursor)
    files = listing["files"]
    skipped_count = len(listing["skipped"])
    walked = 0

    records: list[FeedRecord] = []
    all_ids: list[str] = []
    lines_out: list[str] = []
    changed = 0
    unchanged = 0
    unreadable = 0
    for f in files:
        walked += 1
        rel = str(f["rel"])
        mtime = str(f["mtime_ns"])
        size = str(f["size"])
        at = str(f["iso"])
        path = str(f["path"])
        known = rel in previous
        if known:
            old = previous[rel].split("\t")
            if old[1] == mtime and old[2] == size:
                lines_out.append(previous[rel])
                all_ids.extend(ids_of(old[4]))
                unchanged += 1
                continue

        read_status, text = feed_edge.read_text(path)
        if read_status != "ok":
            unreadable += 1
            if known:
                lines_out.append(previous[rel])
                all_ids.extend(ids_of(previous[rel].split("\t")[4]))
            continue
        digest = sha256_hex(text)
        if known:
            old = previous[rel].split("\t")
            if old[3] == digest:
                ids = ids_of(old[4])
                lines_out.append(cursor_line(rel, mtime, size, digest, ids))
                all_ids.extend(ids)
                unchanged += 1
                continue

        sections = sections_of(rel, path, at, text)
        ids = [s.id for s in sections]
        records.extend(sections)
        all_ids.extend(ids)
        lines_out.append(cursor_line(rel, mtime, size, digest, ids))
        changed += 1

    body = "\n".join(lines_out)
    note = (
        str(walked) + " files: " + str(changed) + " read, " + str(unchanged) + " unchanged, "
        + str(skipped_count) + " skipped (size, name or symlink), " + str(unreadable) + " unreadable"
    )
    return FeedRead(records, all_ids, True, body, len(all_ids), note)
