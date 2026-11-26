"""The git feed: the first-parent commits of one branch of a repository.

Cursor: "<branch>:<full sha>", the tip the last read ended at. A read from a cursor that is still
an ancestor of the tip reads only the commits after it. A cursor that is not (the history was
rewritten, or the branch was reset) makes a full scan of the branch, which lists every commit it
has, so the stored records not among them are retracted.

Record: id is the first 10 hex digits of the sha (fixed, unlike git's %h, which grows with the
repository). text is the subject, the commit's own message, the id, and the changed files with
their line counts (a diffstat, not the diff). links are the parents' ids and "project:<top-level
folder>" for each folder the commit touched.

Port of mojo/src/feed_git.mojo.
"""

from __future__ import annotations

from vinci_edge import feed_edge

from .feed_store import GIT_SOURCE, FeedRead, FeedRecord, feed_record

SHA_ID_CHARS = 10
MAX_BODY_BYTES = 2000
MAX_FILES_LISTED = 40
MAX_PROJECTS = 8


def short_id(sha: str) -> str:
    """The first SHA_ID_CHARS digits of a commit sha."""
    return sha[:SHA_ID_CHARS]


def top_folder(path: str) -> str:
    """The first folder of a path, or "(root)" for a file at the top."""
    slash = path.find("/")
    if slash < 0:
        return "(root)"
    return path[:slash]


def commit_record(c: dict) -> FeedRecord:
    """One commit as a record: see the header for the text and links."""
    sha = str(c["sha"])
    ident = short_id(sha)
    subject = str(c["subject"])
    body = str(c["body"])
    at = str(c["at"])

    total_files = 0
    added = 0
    deleted = 0
    projects: list[str] = []
    listing = ""
    for f in c["files"]:
        path = str(f["path"])
        a = int(f["added"])
        d = int(f["deleted"])
        total_files += 1
        added += a
        deleted += d
        if top_folder(path) not in projects:
            projects.append(top_folder(path))
        if total_files <= MAX_FILES_LISTED:
            listing += "  " + path + "  +" + str(a) + " -" + str(d) + "\n"
    if total_files > MAX_FILES_LISTED:
        listing += "  ... and " + str(total_files - MAX_FILES_LISTED) + " more\n"

    text = subject + "\n\n"
    if len(body) > 0:
        if len(body.encode("utf-8")) > MAX_BODY_BYTES:
            text += body.encode("utf-8")[:MAX_BODY_BYTES].decode("utf-8", errors="ignore") + " ...\n\n"
        else:
            text += body + "\n\n"
    text += "commit " + ident + "\n"
    noun = " file, +" if total_files == 1 else " files, +"
    text += "Changed " + str(total_files) + noun + str(added) + " -" + str(deleted) + "\n"
    text += listing

    links = [short_id(str(p)) for p in c["parents"]]
    for name in projects[:MAX_PROJECTS]:
        links.append("project:" + name)

    return feed_record(GIT_SOURCE, ident, at, "commit", subject, "", text, links)


def cursor_sha(cursor: str, branch: str) -> str:
    """The sha in a "<branch>:<sha>" cursor, or "" when the cursor is for another branch or
    empty."""
    want = branch + ":"
    if not cursor.startswith(want):
        return ""
    return cursor[len(want) :]


def read_git(repo: str, branch: str, cursor: str) -> FeedRead:
    """The commits since cursor on branch, or all of them when the cursor no longer applies.
    Raises when the repository or the branch cannot be read, so the run reports it."""
    head_status, head = feed_edge.git_head(repo, branch)
    if head_status != "ok":
        raise ValueError("git: " + str(head))
    tip = branch + ":" + head
    since = cursor_sha(cursor, branch)

    if since != "" and since == head:
        return FeedRead([], [], False, tip, 0, "no new commits")

    note = "commits since the cursor"
    if since != "":
        anc_status, anc = feed_edge.git_is_ancestor(repo, since, head)
        if anc_status != "ok":
            raise ValueError("git: " + str(anc))
        if anc == "yes":
            range_status, commits = feed_edge.git_commits(repo, since + ".." + head)
            if range_status != "ok":
                raise ValueError("git: " + str(commits))
            found = [short_id(str(c["sha"])) for c in commits]
            return FeedRead([commit_record(c) for c in commits], [], False, tip, len(found), note)
        note = "history was rewritten, so every commit was read again"

    full_status, commits = feed_edge.git_commits(repo, head)
    if full_status != "ok":
        raise ValueError("git: " + str(commits))
    ids = [short_id(str(c["sha"])) for c in commits]
    return FeedRead(
        [commit_record(c) for c in commits],
        ids,
        True,
        tip,
        len(ids),
        note if since != "" else "first read of the branch",
    )
