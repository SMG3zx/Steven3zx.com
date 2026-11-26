"""The read tool's text work, as Reading.cs and Tools.Read do it, minus the memory service.

A text file (.txt, .md, .markdown) or a web page that the person named becomes passages of at most
1,200 characters, saved as one body file under docs/<sha256>.md. The journal's read line points at
that file, and the index reads its passages from it. Stdlib only.
"""

from __future__ import annotations

import hashlib
import html
import os
import pwd
import re
import urllib.error
import urllib.parse
import urllib.request
from datetime import UTC, datetime
from pathlib import Path
from typing import Final

from .read_guard import (
    Refused,
    check_local_path,
    deny_list,
    describe_fetch_error,
    guarded_opener,
    scheme_problem,
)

PASSAGE_CHARS: Final = 1200
MAX_BYTES: Final = 1_000_000
MAX_PASSAGES: Final = 500
FETCH_TIMEOUT_S: Final = 30
TEXT_EXTENSIONS: Final = (".txt", ".md", ".markdown")
# Refusals and fetch errors are read_guard.Refused: its message is what the tool tells the model.

_BLANK: Final = re.compile(r"\n\s*\n")
_HIDDEN: Final = re.compile(r"<(script|style)\b[^>]*>.*?</\1\s*>", re.IGNORECASE | re.DOTALL)
_TAG: Final = re.compile(r"<[^>]+>")
_TITLE: Final = re.compile(r"<title[^>]*>(.*?)</title\s*>", re.IGNORECASE | re.DOTALL)


def passages_of(text: str) -> list[str]:
    """Cuts text at blank lines into passages of at most PASSAGE_CHARS. A paragraph longer than
    that is cut into pieces, and a passage packs as many paragraphs as fit."""
    passages: list[str] = []
    current = ""
    for raw in _BLANK.split(text.replace("\r\n", "\n")):
        paragraph = raw.strip()
        if paragraph == "":
            continue
        for start in range(0, len(paragraph), PASSAGE_CHARS):
            part = paragraph[start : start + PASSAGE_CHARS]
            if current != "" and len(current) + 2 + len(part) > PASSAGE_CHARS:
                passages.append(current)
                current = ""
            if current != "":
                current += "\n\n"
            current += part
    if current != "":
        passages.append(current)
    return passages


def html_to_text(page: str) -> str:
    """The readable text of a page: scripts and styles dropped, tags replaced by line breaks,
    entities decoded."""
    return html.unescape(_TAG.sub("\n", _HIDDEN.sub(" ", page)))


def html_title(page: str) -> str | None:
    """The page's title, or None when it has none."""
    found = _TITLE.search(page)
    if found is None:
        return None
    return html.unescape(found.group(1)).strip()


def utc_date(now_iso: str) -> str:
    """The UTC calendar date of an ISO 8601 instant (yyyy-MM-dd)."""
    return datetime.fromisoformat(now_iso).astimezone(UTC).date().isoformat()


def account_home() -> str:
    """The home folder of the account that runs vinci, from the password database. HOME is not
    used: anything that sets it (a test, a wrapper) would move the boundary of the read tool."""
    return pwd.getpwuid(os.getuid()).pw_dir


def _from_file(path: str, root: str, deny: list[str]) -> tuple[str, str, str]:
    """A text file under the person's home folder, and not in a secret folder (read_guard)."""
    full = os.path.abspath(os.path.expanduser(path))
    if not full.lower().endswith(TEXT_EXTENSIONS):
        raise Refused("only .txt, .md and .markdown files can be read for now")
    resolved = check_local_path(full, root, deny)
    if not os.path.isfile(resolved):
        raise Refused(f"there is no file at {full}")
    if os.path.getsize(resolved) > MAX_BYTES:
        raise Refused("the file is larger than 1 MB")
    with open(resolved, encoding="utf-8", errors="replace") as handle:
        text = handle.read()
    return (Path(resolved).stem, text, resolved)


def _from_url(url: str) -> tuple[str, str, str]:
    """A web page: http or https only, every address checked, every redirect checked again
    (read_guard.guarded_opener)."""
    problem = scheme_problem(url)
    if problem is not None:
        raise Refused(problem)
    parts = urllib.parse.urlsplit(url)
    request = urllib.request.Request(url, headers={"User-Agent": "vinci"})
    try:
        with guarded_opener().open(request, timeout=FETCH_TIMEOUT_S) as response:
            media = response.headers.get_content_type()
            if not media.startswith("text/"):
                raise Refused(f"the page is {media}, not text")
            body = response.read(MAX_BYTES + 1)
    except Refused:
        raise
    except (urllib.error.URLError, OSError) as err:
        raise Refused(describe_fetch_error(err)) from err
    if len(body) > MAX_BYTES:
        raise Refused("the page is larger than 1 MB")
    page = body.decode("utf-8", errors="replace")
    if media == "text/html":
        title = html_title(page) or (parts.netloc + parts.path)
        return (title, html_to_text(page), url)
    return (parts.netloc + parts.path, page, url)


def read_source(
    target: str,
    is_url: bool,
    home: str,
    now_iso: str,
    person_home: str | None = None,
) -> dict[str, str]:
    """Reads a file or page, saves its passages as a body file under home, and describes it.

    A file must lie under person_home (the person's home folder, by default) and outside the
    secret folders (read_guard.deny_list), with symbolic links and .. resolved first.

    Returns {"status": "ok", "title", "locator", "date", "count", "where"} or
    {"status": "error", "error": message}. where is the body file's path under home.
    """
    root = person_home if person_home is not None else account_home()
    try:
        if is_url:
            title, text, locator = _from_url(target)
        else:
            title, text, locator = _from_file(target, root, deny_list(home, root))
        passages = passages_of(text)
        if len(passages) == 0:
            raise Refused("there is no text in it")
        if len(passages) > MAX_PASSAGES:
            raise Refused(f"it has {len(passages)} passages; the limit is {MAX_PASSAGES}")
    except Refused as refused:
        return {"status": "error", "error": str(refused)}
    body = "\n\n".join(passages).encode("utf-8")
    digest = hashlib.sha256(body).hexdigest()
    where = f"docs/{digest}.md"
    target_path = Path(home) / where
    target_path.parent.mkdir(parents=True, exist_ok=True)
    target_path.write_bytes(body)
    return {
        "status": "ok",
        "title": title,
        "locator": locator,
        "date": utc_date(now_iso),
        "count": str(len(passages)),
        "where": where,
    }
