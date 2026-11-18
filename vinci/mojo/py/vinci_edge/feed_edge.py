"""The feeds' reads from outside vinci: git (read-only), Markdown folders, feeds.toml, and the
journal's bytes. Mojo does the records, the cursors and the versions (src/feed*.mojo).

Each function returns a status word first, so Mojo never parses a Python exception:
("ok", value), ("error", message) or ("missing", ""). Standard library only, except tomllib,
which is in the standard library since Python 3.11.
"""

from __future__ import annotations

import hashlib
import os
import subprocess
import tomllib
from datetime import datetime
from pathlib import Path
from typing import Final, cast

COLD_STORAGE: Final = "cold-storage"
# Folders never read. cold-storage is the repository's rule (janus/cold-storage/README.md):
# it is not configurable, and a root inside it is refused.
ALWAYS_SKIP: Final = frozenset(
    {
        COLD_STORAGE,
        "node_modules",
        "__pycache__",
        "build",
        "dist",
        "target",
        "bin",
        "obj",
        "out",
    }
)
MARKDOWN_SUFFIXES: Final = (".md", ".markdown")
DEFAULT_MAX_BYTES: Final = 512 * 1024
GIT_TIMEOUT_S: Final = 120
# One record per commit: fields separated by 0x1f, the header ended by 0x1d, and the
# numstat lines after it. 0x1e starts each commit.
GIT_LOG_FORMAT: Final = "%x1e%H%x1f%P%x1f%cI%x1f%s%x1f%b%x1d"
CONFIG_KEYS: Final = {
    "git": frozenset({"enabled", "path", "branch"}),
    "vaults": frozenset({"enabled", "roots", "max_bytes", "exclude"}),
}


def _git(repo: str, *args: str) -> subprocess.CompletedProcess[str]:
    """One read-only git call in repo. Output is UTF-8; bad bytes are replaced."""
    return subprocess.run(
        ["git", "-C", repo, "-c", "core.quotepath=off", *args],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
        timeout=GIT_TIMEOUT_S,
    )


def git_head(repo: str, branch: str) -> tuple[str, str]:
    """The full sha of the branch's tip, or an error when the branch does not exist."""
    try:
        done = _git(repo, "rev-parse", "--verify", "--quiet", f"refs/heads/{branch}^{{commit}}")
    except (OSError, subprocess.SubprocessError) as err:
        return ("error", str(err))
    if done.returncode != 0:
        return ("error", f"no branch {branch} in {repo}: {done.stderr.strip()}")
    return ("ok", done.stdout.strip())


def git_is_ancestor(repo: str, ancestor: str, head: str) -> tuple[str, str]:
    """ "yes" when ancestor is reachable from head, "no" when not, else an error."""
    try:
        done = _git(repo, "merge-base", "--is-ancestor", ancestor, head)
    except (OSError, subprocess.SubprocessError) as err:
        return ("error", str(err))
    if done.returncode == 0:
        return ("ok", "yes")
    if done.returncode == 1:
        return ("ok", "no")
    return ("error", done.stderr.strip())


def git_commits(repo: str, rev: str) -> tuple[str, object]:
    """The first-parent commits reachable from rev (a sha, or a range sha..sha), newest
    first. Each is a dict: sha, parents, at (committer time, ISO 8601), subject, body and
    files (dicts with path, added, deleted). Merges show their diff against the first
    parent, which is what --first-parent shows."""
    try:
        done = _git(
            repo,
            "log",
            "--first-parent",
            "--no-renames",
            "--numstat",
            f"--format={GIT_LOG_FORMAT}",
            rev,
        )
    except (OSError, subprocess.SubprocessError) as err:
        return ("error", str(err))
    if done.returncode != 0:
        return ("error", done.stderr.strip())
    commits: list[dict[str, object]] = []
    for chunk in done.stdout.split("\x1e")[1:]:
        header, _, numstat = chunk.partition("\x1d")
        sha, parents, at, subject, body = header.split("\x1f", 4)
        files: list[dict[str, object]] = []
        for line in numstat.splitlines():
            parts = line.split("\t")
            if len(parts) != 3:
                continue
            added, deleted, path = parts
            files.append(
                {
                    "path": path,
                    "added": 0 if added == "-" else int(added),
                    "deleted": 0 if deleted == "-" else int(deleted),
                }
            )
        commits.append(
            {
                "sha": sha,
                "parents": parents.split(),
                "at": at,
                "subject": subject,
                "body": body.strip(),
                "files": files,
            }
        )
    return ("ok", commits)


def _skip_dir(name: str, extra: frozenset[str]) -> bool:
    return name in ALWAYS_SKIP or name in extra or name.startswith(".")


def list_markdown(roots: list[str], exclude: list[str], max_bytes: int) -> tuple[str, object]:
    """Every Markdown file under the roots, as a dict: files (rel, mtime_ns, size, iso,
    path) and skipped (rel, reason). rel is the path from the root's parent folder, so
    two roots never share an id. Folders named cold-storage, hidden folders and the
    build and dependency folders are not entered. Symlinks are not followed."""
    extra = frozenset(exclude)
    files: list[dict[str, object]] = []
    skipped: list[dict[str, str]] = []
    for root in roots:
        root_path = Path(root)
        if COLD_STORAGE in root_path.parts:
            return ("error", f"cold-storage is never read: {root}")
        if not root_path.is_dir():
            return ("error", f"not a folder: {root}")
        base = root_path.parent
        for dirpath, dirnames, filenames in os.walk(root_path):
            dirnames[:] = sorted(d for d in dirnames if not _skip_dir(d, extra))
            for name in sorted(filenames):
                if not name.lower().endswith(MARKDOWN_SUFFIXES):
                    continue
                full = Path(dirpath) / name
                rel = full.relative_to(base).as_posix()
                if full.is_symlink():
                    skipped.append({"rel": rel, "reason": "symlink"})
                    continue
                if any(c in rel for c in "\t\n|"):
                    skipped.append({"rel": rel, "reason": "name"})
                    continue
                st = full.stat()
                if st.st_size > max_bytes:
                    skipped.append({"rel": rel, "reason": "size"})
                    continue
                iso = datetime.fromtimestamp(st.st_mtime).astimezone().isoformat(timespec="seconds")
                files.append(
                    {
                        "rel": rel,
                        "mtime_ns": st.st_mtime_ns,
                        "size": st.st_size,
                        "iso": iso,
                        "path": str(full),
                    }
                )
    return ("ok", {"files": files, "skipped": skipped})


def read_text(path: str) -> tuple[str, str]:
    """The file as UTF-8 text, or an error (not UTF-8, unreadable)."""
    try:
        return ("ok", Path(path).read_bytes().decode("utf-8"))
    except (OSError, UnicodeDecodeError) as err:
        return ("error", str(err))


def journal_split(path: str, offset: int) -> tuple[str, str, str]:
    """The sha-256 of the journal's first offset bytes, and the text after them. The hash
    is what a rewritten journal changes. A missing file is ("missing", "", "")."""
    try:
        data = Path(path).read_bytes()
    except FileNotFoundError:
        return ("missing", "", "")
    except OSError as err:
        return ("error", str(err), "")
    prefix = hashlib.sha256(data[:offset]).hexdigest()
    try:
        return ("ok", prefix, data[offset:].decode("utf-8"))
    except UnicodeDecodeError as err:
        return ("error", str(err), "")


def journal_prefix(path: str, length: int) -> tuple[str, str]:
    """The sha-256 of the journal's first length bytes."""
    try:
        data = Path(path).read_bytes()
    except OSError as err:
        return ("error", str(err))
    return ("ok", hashlib.sha256(data[:length]).hexdigest())


def _table(raw: dict[str, object], name: str) -> dict[str, object]:
    """The table [name] of the TOML document, or an empty one when it is absent."""
    value = raw.get(name, {})
    if not isinstance(value, dict):
        raise TypeError(f"[{name}] must be a table")
    return cast("dict[str, object]", value)


def _strings(value: object, name: str) -> list[str]:
    if not isinstance(value, list):
        raise TypeError(f"{name} must be a list of strings")
    items = cast("list[object]", value)
    if not all(isinstance(v, str) for v in items):
        raise TypeError(f"{name} must be a list of strings")
    return [str(v) for v in items]


def feed_config(path: str) -> tuple[str, object]:
    """The feeds settings, with the defaults filled in. The file is optional. Its tables
    are [git] and [vaults], and an unknown key is an error, so a typo does not pass."""
    home = Path.home()
    git: dict[str, object] = {
        "enabled": True,
        "path": str(home / "Steven3zx.com"),
        "branch": "master",
    }
    vaults: dict[str, object] = {
        "enabled": True,
        "roots": [
            str(home / "Steven3zx.com" / "Meingrad"),
            str(home / "Steven3zx.com" / "Research"),
            str(home / "Steven3zx.com" / "vinci" / "docs"),
        ],
        "max_bytes": DEFAULT_MAX_BYTES,
        "exclude": [],
    }
    config: dict[str, object] = {"git": git, "vaults": vaults, "from_file": False}
    if not Path(path).exists():
        return ("ok", config)
    try:
        raw = cast("dict[str, object]", tomllib.loads(Path(path).read_text(encoding="utf-8")))
    except (OSError, UnicodeDecodeError, tomllib.TOMLDecodeError) as err:
        return ("error", f"{path}: {err}")
    try:
        for table, keys in CONFIG_KEYS.items():
            for key in _table(raw, table):
                if key not in keys:
                    raise ValueError(f"[{table}] has no key {key!r}")
        git_raw = _table(raw, "git")
        vault_raw = _table(raw, "vaults")
        if "enabled" in git_raw:
            git["enabled"] = bool(git_raw["enabled"])
        if "path" in git_raw:
            git["path"] = str(Path(str(git_raw["path"])).expanduser())
        if "branch" in git_raw:
            git["branch"] = str(git_raw["branch"])
        if "enabled" in vault_raw:
            vaults["enabled"] = bool(vault_raw["enabled"])
        if "roots" in vault_raw:
            vaults["roots"] = [
                str(Path(r).expanduser()) for r in _strings(vault_raw["roots"], "roots")
            ]
        if "max_bytes" in vault_raw:
            max_bytes = vault_raw["max_bytes"]
            if not isinstance(max_bytes, int):
                raise TypeError("max_bytes must be a whole number")
            vaults["max_bytes"] = max_bytes
        if "exclude" in vault_raw:
            vaults["exclude"] = _strings(vault_raw["exclude"], "exclude")
    except (ValueError, TypeError) as err:
        return ("error", f"{path}: {err}")
    config["from_file"] = True
    return ("ok", config)
