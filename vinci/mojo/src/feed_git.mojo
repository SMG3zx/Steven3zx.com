# The git feed: the first-parent commits of one branch of a repository (M5 step a).
#
# Cursor: "<branch>:<full sha>", the tip the last read ended at. A read from a cursor that is
# still an ancestor of the tip reads only the commits after it. A cursor that is not (the
# history was rewritten, or the branch was reset) makes a full scan of the branch, which
# lists every commit it has, so the stored records not among them are retracted.
#
# Record: id is the first 10 hex digits of the sha (fixed, unlike git's %h, which grows with
# the repository). text is the subject, the commit's own message, the id, and the changed
# files with their line counts (a diffstat, not the diff). links are the parents' ids and
# "project:<top-level folder>" for each folder the commit touched.

from std.python import PythonObject

from feed_edge import git_commits, git_head, git_is_ancestor
from feed_store import FeedRead, FeedRecord, GIT_SOURCE, feed_record
from open_view import utf8_prefix
from text import ascii_tail

comptime SHA_ID_CHARS = 10
comptime MAX_BODY_BYTES = 2000
comptime MAX_FILES_LISTED = 40
comptime MAX_PROJECTS = 8


def short_id(sha: String) raises -> String:
    """The first SHA_ID_CHARS digits of a commit sha."""
    return utf8_prefix(sha, SHA_ID_CHARS)


def top_folder(path: String) -> String:
    """The first folder of a path, or "(root)" for a file at the top."""
    var slash = path.find("/")
    if slash < 0:
        return "(root)"
    return utf8_prefix(path, slash)


def add_unique(mut items: List[String], value: String):
    for item in items:
        if item == value:
            return
    items.append(value)


def commit_record(c: PythonObject) raises -> FeedRecord:
    """One commit as a record: see the header for the text and links."""
    var sha = String(c["sha"])
    var id = short_id(sha)
    var subject = String(c["subject"])
    var body = String(c["body"])
    var at = String(c["at"])

    var files = c["files"]
    var total_files = 0
    var added = 0
    var deleted = 0
    var projects = List[String]()
    var listing = String()
    for f in files:
        var path = String(f["path"])
        var a = Int(String(f["added"]))
        var d = Int(String(f["deleted"]))
        total_files += 1
        added += a
        deleted += d
        add_unique(projects, top_folder(path))
        if total_files <= MAX_FILES_LISTED:
            listing += "  " + path + "  +" + String(a) + " -" + String(d) + "\n"
    if total_files > MAX_FILES_LISTED:
        listing += (
            "  ... and " + String(total_files - MAX_FILES_LISTED) + " more\n"
        )

    var text = subject + "\n\n"
    if body.byte_length() > 0:
        if body.byte_length() > MAX_BODY_BYTES:
            text += utf8_prefix(body, MAX_BODY_BYTES) + " ...\n\n"
        else:
            text += body + "\n\n"
    text += "commit " + id + "\n"
    var noun = String(" file, +") if total_files == 1 else String(" files, +")
    text += (
        "Changed "
        + String(total_files)
        + noun
        + String(added)
        + " -"
        + String(deleted)
        + "\n"
    )
    text += listing

    var links = List[String]()
    for p in c["parents"]:
        links.append(short_id(String(p)))
    var project_count = 0
    for name in projects:
        if project_count >= MAX_PROJECTS:
            break
        links.append("project:" + name)
        project_count += 1

    return feed_record(
        GIT_SOURCE,
        id,
        at,
        "commit",
        subject,
        "",
        text^,
        links,
    )


def all_ids_of(commits: PythonObject) raises -> List[String]:
    var out = List[String]()
    for c in commits:
        out.append(short_id(String(c["sha"])))
    return out^


def records_of(commits: PythonObject) raises -> List[FeedRecord]:
    var out = List[FeedRecord]()
    for c in commits:
        out.append(commit_record(c))
    return out^


def cursor_sha(cursor: String, branch: String) -> String:
    """The sha in a "<branch>:<sha>" cursor, or "" when the cursor is for another branch
    or empty."""
    var want = branch + ":"
    if not cursor.startswith(want):
        return String()
    return String(ascii_tail(cursor, want.byte_length()))


def read_git(repo: String, branch: String, cursor: String) raises -> FeedRead:
    """The commits since cursor on branch, or all of them when the cursor no longer applies.
    Raises when the repository or the branch cannot be read, so the run reports it.
    """
    var head_reply = git_head(repo, branch)
    if head_reply.status != "ok":
        raise Error("git: " + String(head_reply.value))
    var head = String(head_reply.value)
    var tip = branch + ":" + head
    var since = cursor_sha(cursor, branch)

    if since.byte_length() > 0 and since == head:
        return FeedRead(
            List[FeedRecord](), List[String](), False, tip, 0, "no new commits"
        )

    var note = String("commits since the cursor")
    if since.byte_length() > 0:
        var ancestor = git_is_ancestor(repo, since, head)
        if ancestor.status != "ok":
            raise Error("git: " + String(ancestor.value))
        if String(ancestor.value) == "yes":
            var range_reply = git_commits(repo, since + ".." + head)
            if range_reply.status != "ok":
                raise Error("git: " + String(range_reply.value))
            var commits = range_reply.value
            var found = all_ids_of(commits)
            return FeedRead(
                records_of(commits),
                List[String](),
                False,
                tip,
                len(found),
                note,
            )
        note = "history was rewritten, so every commit was read again"

    var full = git_commits(repo, head)
    if full.status != "ok":
        raise Error("git: " + String(full.value))
    var commits = full.value
    var ids = all_ids_of(commits)
    var seen = len(ids)
    return FeedRead(
        records_of(commits),
        ids^,
        True,
        tip,
        seen,
        note if since.byte_length() > 0 else "first read of the branch",
    )
