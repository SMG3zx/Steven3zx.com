#!/usr/bin/env python3
"""Small standard-library project catalog and task runner for the workspace."""

from __future__ import annotations

import argparse
import json
import math
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


TOOL_DIR = Path(__file__).resolve().parent
WORKSPACE = TOOL_DIR.parent
CONFIG = TOOL_DIR / "projects.json"

# Keep patterns focused on recognizable credential formats and assignments.
# The private-key marker is assembled to avoid matching this source file itself.
SECRET_PATTERNS: tuple[tuple[str, re.Pattern[bytes]], ...] = (
    ("private key block", re.compile(rb"(?m)^[ \t]*-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----[ \t]*$")),
    ("GitHub token", re.compile(rb"\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b")),
    ("AWS access key", re.compile(rb"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b")),
    ("Slack token", re.compile(rb"\bxox[baprs]-[A-Za-z0-9-]{20,}\b")),
    ("OpenAI-style API key", re.compile(rb"\bsk-[A-Za-z0-9_-]{32,}\b")),
    ("JWT", re.compile(rb"\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b")),
    (
        "high-risk assignment",
        re.compile(
            rb"(?i)\b(?:api[_-]?key|access[_-]?token|client[_-]?secret|password|secret[_-]?key)"
            rb"\b\s*[:=]\s*[\"']([^\"'\r\n]{16,})[\"']"
        ),
    ),
)
TEXT_SUFFIXES = {
    ".c", ".cfg", ".conf", ".cpp", ".cs", ".css", ".env", ".go", ".h", ".html",
    ".ini", ".java", ".js", ".json", ".jsx", ".md", ".mjs", ".properties", ".py",
    ".rs", ".sh", ".sql", ".toml", ".ts", ".tsx", ".txt", ".xml", ".yaml", ".yml", ".zig",
}
PLACEHOLDER_VALUES = {
    "changeme", "change-me", "example", "fake", "dummy", "placeholder", "your-token-here",
    "your_api_key", "your-api-key", "replace-me", "test", "testing",
}


def looks_high_entropy(value: str) -> bool:
    """Avoid flagging low-entropy test examples and documentation placeholders."""
    if len(value) < 20:
        return False
    frequencies = {character: value.count(character) / len(value) for character in set(value)}
    entropy = -sum(frequency * math.log2(frequency) for frequency in frequencies.values())
    return entropy >= 3.5


def load_projects() -> dict[str, dict[str, Any]]:
    try:
        value = json.loads(CONFIG.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SystemExit(f"Cannot read {CONFIG}: {exc}") from exc
    projects = value.get("projects")
    if not isinstance(projects, dict):
        raise SystemExit(f"{CONFIG} must contain a 'projects' object")
    return projects


def project_path(name: str, projects: dict[str, dict[str, Any]]) -> Path:
    if name not in projects:
        raise SystemExit(f"Unknown project '{name}'. Run: python meingrad.py list")
    rel = Path(projects[name]["path"])
    path = (WORKSPACE / rel).resolve()
    if path != WORKSPACE.resolve() and WORKSPACE.resolve() not in path.parents:
        raise SystemExit(f"Project path escapes workspace: {rel}")
    if not path.is_dir():
        raise SystemExit(f"Project directory does not exist: {path}")
    return path


def git_root() -> Path | None:
    result = subprocess.run(
        ["git", "-C", str(WORKSPACE), "rev-parse", "--show-toplevel"],
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode:
        return None
    return Path(result.stdout.strip()).resolve()


def run_list(projects: dict[str, dict[str, Any]]) -> int:
    print(f"Workspace: {WORKSPACE}")
    root = git_root()
    print(f"Git root:  {root if root else '(not initialized)'}\n")
    width = max((len(name) for name in projects), default=7)
    print(f"{'PROJECT':<{width}}  {'STATE':<9}  {'LANGUAGE':<14}  PATH")
    for name, spec in projects.items():
        exists = (WORKSPACE / spec["path"]).is_dir()
        state = spec.get("state", "active")
        print(f"{name:<{width}}  {state if exists else 'missing':<9}  {spec.get('language', 'unknown'):<14}  {spec['path']}")
    return 0


def run_task(name: str, task: str, projects: dict[str, dict[str, Any]]) -> int:
    spec = projects.get(name)
    if spec is None:
        raise SystemExit(f"Unknown project '{name}'. Run: python meingrad.py list")
    path = project_path(name, projects)
    command = spec.get("tasks", {}).get(task)
    if not command:
        tasks = ", ".join(sorted(spec.get("tasks", {}))) or "none configured"
        raise SystemExit(f"No '{task}' task for {name}. Available tasks: {tasks}")
    if not isinstance(command, list) or not all(isinstance(part, str) for part in command):
        raise SystemExit(f"Task {name}.{task} must be an argument array in {CONFIG}")
    print(f"meingrad: {name} {task}: {' '.join(command)}", flush=True)
    try:
        return subprocess.run(command, cwd=path, check=False).returncode
    except FileNotFoundError as exc:
        print(f"meingrad: command not found: {exc.filename}", file=sys.stderr)
        return 127


def git_changed_projects(projects: dict[str, dict[str, Any]], base: str | None) -> int:
    root = git_root()
    if root is None:
        print("meingrad: no Git repository found; initialize one before using changed-project selection", file=sys.stderr)
        return 2
    command = ["git", "-C", str(root), "status", "--short", "--untracked-files=all"]
    if base:
        command = ["git", "-C", str(root), "diff", "--name-only", "--diff-filter=ACMR", f"{base}...HEAD"]
    result = subprocess.run(command, text=True, capture_output=True, check=False)
    if result.returncode:
        print(result.stderr, file=sys.stderr, end="")
        return result.returncode
    paths: set[str] = set()
    for line in result.stdout.splitlines():
        # git status --short has a two-column status prefix; diff --name-only does not.
        path = line[3:] if not base and len(line) >= 4 else line
        path = path.strip().strip('"').replace("\\", "/")
        if " -> " in path:
            path = path.split(" -> ", 1)[1]
        paths.add(path)
    changed = []
    for name, spec in projects.items():
        prefix = str(spec["path"]).replace("\\", "/").rstrip("/") + "/"
        if any(path.startswith(prefix) for path in paths):
            changed.append(name)
    if changed:
        print("\n".join(changed))
    else:
        print("(no project changes detected)")
    return 0


def check_safety(projects: dict[str, dict[str, Any]]) -> int:
    root = git_root()
    if root is None:
        print("meingrad: no Git repository found; safety check cannot inspect tracked or staged files", file=sys.stderr)
        return 2
    result = subprocess.run(
        ["git", "-C", str(root), "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        capture_output=True,
        check=False,
    )
    if result.returncode:
        print("meingrad: git ls-files failed", file=sys.stderr)
        return result.returncode
    paths = [item.decode("utf-8", errors="replace") for item in result.stdout.split(b"\0") if item]
    unsafe: list[tuple[str, str]] = []
    exact_names = {".env", ".env.local", ".env.production", "id_rsa", "id_ed25519"}
    forbidden_parts = {"node_modules", "target", "dist", "build", ".zig-cache", "zig-cache", "zig-out", "zig-pkg", "__pycache__", ".pytest_cache", "playwright-report", "test-results"}
    forbidden_suffixes = {".db", ".sqlite", ".sqlite3", ".etl", ".log", ".vhdx"}
    for rel in paths:
        path = Path(rel)
        parts = set(path.parts)
        if path.name in exact_names or (path.name.startswith(".env.") and path.name != ".env.example"):
            unsafe.append((rel, "secret or environment file"))
        elif parts & forbidden_parts:
            unsafe.append((rel, "generated/dependency output"))
        elif path.suffix.lower() in forbidden_suffixes:
            unsafe.append((rel, "runtime data or machine log"))
    if unsafe:
        print("Potentially unsafe files:")
        for path, reason in unsafe:
            print(f"  {path} ({reason})")
        print(f"\n{len(unsafe)} finding(s). Review before committing; remove or ignore files as appropriate.")
        return 1
    print(f"Safety check passed ({len(paths)} Git-visible paths checked).")
    return 0


def git_visible_paths(root: Path) -> tuple[list[str] | None, int]:
    result = subprocess.run(
        ["git", "-C", str(root), "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        capture_output=True,
        check=False,
    )
    if result.returncode:
        print("meingrad: git ls-files failed", file=sys.stderr)
        if result.stderr:
            print(result.stderr.decode("utf-8", errors="replace"), file=sys.stderr, end="")
        return None, result.returncode
    return [item.decode("utf-8", errors="replace") for item in result.stdout.split(b"\0") if item], 0


def scan_secrets(root: Path) -> int:
    paths, code = git_visible_paths(root)
    if paths is None:
        return code
    findings: list[tuple[str, str]] = []
    scanned = 0
    skipped_large = 0
    for rel in paths:
        path = root / rel
        if path.suffix.lower() not in TEXT_SUFFIXES and path.name.lower() not in {"dockerfile", "makefile"}:
            continue
        try:
            if path.stat().st_size > 4 * 1024 * 1024:
                skipped_large += 1
                continue
            content = path.read_bytes()
        except OSError:
            findings.append((rel, "unreadable file"))
            continue
        if b"\0" in content:
            continue
        scanned += 1
        for label, pattern in SECRET_PATTERNS:
            match = pattern.search(content)
            if not match:
                continue
            # Generic assignments are only reported when they don't look like
            # documented placeholders. Values are never included in output.
            if label == "high-risk assignment":
                # Test suites intentionally contain synthetic credentials and
                # redaction inputs. Strong provider-token/private-key formats
                # above are still scanned in tests.
                normalized_rel = rel.replace("\\", "/").lower()
                if "/tests/" in f"/{normalized_rel}/" or "/test/" in f"/{normalized_rel}/":
                    continue
                value = match.group(1).decode("utf-8", errors="ignore").strip().lower()
                if value in PLACEHOLDER_VALUES or value.startswith(("${", "$env:", "<", "your_", "your-")):
                    continue
                if not looks_high_entropy(value):
                    continue
            findings.append((rel, label))
            break
    if findings:
        print("Secret scan found candidates (values are suppressed):")
        for path, label in findings:
            print(f"  {path} ({label})")
        print(f"Secret scan failed: {len(findings)} candidate(s), {scanned} text file(s) scanned.")
        if skipped_large:
            print(f"Skipped {skipped_large} text file(s) larger than 4 MiB.")
        print("Review each path; scanner matches can include test fixtures and examples.")
        return 1
    print(f"Secret scan passed: {scanned} text file(s) scanned; matched values are never printed.")
    if skipped_large:
        print(f"Skipped {skipped_large} text file(s) larger than 4 MiB.")
    return 0


def run_ci(projects: dict[str, dict[str, Any]], selected: str | None, security_only: bool) -> int:
    root = git_root()
    if root is None:
        print("meingrad CI: no Git-backed workspace found", file=sys.stderr)
        return 2
    print("[1/3] Repository safety", flush=True)
    result = check_safety(projects)
    if result:
        return result
    print("\n[2/3] Secret scan", flush=True)
    result = scan_secrets(root)
    if result:
        return result
    if security_only:
        print("\nmeingrad CI passed (security gates only).")
        return 0

    if selected and selected not in projects:
        raise SystemExit(f"Unknown project '{selected}'. Run: python meingrad.py list")
    targets = [selected] if selected else list(projects)
    checks: list[tuple[str, str, list[str]]] = []
    for name in targets:
        spec = projects[name]
        for command in spec.get("ci", []):
            if not isinstance(command, dict) or not isinstance(command.get("name"), str):
                raise SystemExit(f"Each {name}.ci entry needs a string 'name'")
            argv = command.get("command")
            if not isinstance(argv, list) or not argv or not all(isinstance(part, str) for part in argv):
                raise SystemExit(f"Each {name}.ci command must be a non-empty argument array")
            checks.append((name, command["name"], argv))
    if not checks:
        print("\nNo project CI commands are configured; security gates passed.")
        return 0
    print(f"\n[3/3] Project checks ({len(checks)} command(s))", flush=True)
    failed: list[tuple[str, str, int]] = []
    for index, (name, label, argv) in enumerate(checks, 1):
        path = project_path(name, projects)
        print(f"\n[{index}/{len(checks)}] {name}: {label} ({' '.join(argv)})", flush=True)
        try:
            code = subprocess.run(argv, cwd=path, check=False).returncode
        except FileNotFoundError as exc:
            print(f"meingrad: command not found: {exc.filename}", file=sys.stderr)
            code = 127
        if code:
            failed.append((name, label, code))
    if failed:
        print("\nProject checks failed:")
        for name, label, code in failed:
            print(f"  {name} / {label}: exit {code}")
        return 1
    print("\nmeingrad CI passed.")
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="meingrad", description="Project catalog and task runner")
    sub = parser.add_subparsers(dest="action", required=True)
    sub.add_parser("list", help="list known projects and their state")
    task_parser = sub.add_parser("run", help="run a configured project task")
    task_parser.add_argument("project")
    task_parser.add_argument("task")
    changed_parser = sub.add_parser("changed", help="list projects with local changes")
    changed_parser.add_argument("--base", help="compare committed changes against BASE...HEAD")
    sub.add_parser("check", help="check Git-visible files for likely secrets and generated data")
    ci_parser = sub.add_parser("ci", help="run local safety, secret, and configured project checks")
    ci_parser.add_argument("--project", help="run configured project checks for one project only")
    ci_parser.add_argument("--security-only", action="store_true", help="run repository safety and secret scan only")
    args = parser.parse_args(argv)
    projects = load_projects()
    if args.action == "list":
        return run_list(projects)
    if args.action == "run":
        return run_task(args.project, args.task, projects)
    if args.action == "changed":
        return git_changed_projects(projects, args.base)
    if args.action == "check":
        return check_safety(projects)
    if args.action == "ci":
        return run_ci(projects, args.project, args.security_only)
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
