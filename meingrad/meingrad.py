#!/usr/bin/env python3
"""Small standard-library project catalog and task runner for the workspace."""

from __future__ import annotations

import argparse
import json
import math
import re
import shutil
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
    if not isinstance(value, dict):
        raise SystemExit(f"{CONFIG} must contain a JSON object")
    projects = value.get("projects")
    if not isinstance(projects, dict) or not all(isinstance(name, str) for name in projects):
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


def jj_root() -> Path | None:
    try:
        result = subprocess.run(
            ["jj", "-R", str(WORKSPACE), "root"],
            text=True,
            capture_output=True,
            check=False,
        )
    except FileNotFoundError:
        return None
    if result.returncode:
        return None
    return Path(result.stdout.strip()).resolve()


def jj_command(root: Path, *args: str) -> list[str]:
    return ["jj", "-R", str(root), *args]


def run_list(projects: dict[str, dict[str, Any]]) -> int:
    print(f"Workspace: {WORKSPACE}")
    root = jj_root()
    print(f"JJ root:   {root if root else '(not initialized)'}\n")
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


def changed_paths(base: str | None) -> tuple[set[str] | None, int]:
    root = jj_root()
    if root is None:
        print("meingrad: no Jujutsu repository found; initialize one before using changed-project selection", file=sys.stderr)
        return None, 2
    command = jj_command(root, "diff", "--name-only", "--color", "never")
    if base:
        command.extend(["--from", base, "--to", "@"])
    else:
        command.extend(["-r", "@"])
    result = subprocess.run(command, text=True, capture_output=True, check=False)
    if result.returncode:
        print(result.stderr, file=sys.stderr, end="")
        return None, result.returncode
    return {line.replace("\\", "/") for line in result.stdout.splitlines() if line}, 0


def affected_projects(direct: set[str], projects: dict[str, dict[str, Any]]) -> list[str]:
    affected = set(direct)
    changed = True
    while changed:
        changed = False
        for name, spec in projects.items():
            dependencies = spec.get("depends_on", [])
            if not isinstance(dependencies, list):
                continue
            if name not in affected and any(dependency in affected for dependency in dependencies):
                affected.add(name)
                changed = True
    return [name for name in projects if name in affected]


def changed_project_names(projects: dict[str, dict[str, Any]], base: str | None) -> tuple[list[str] | None, int]:
    paths, code = changed_paths(base)
    if paths is None:
        return None, code
    direct = set()
    for name, spec in projects.items():
        prefix = str(spec["path"]).replace("\\", "/").rstrip("/") + "/"
        if any(path.startswith(prefix) or path == prefix[:-1] for path in paths):
            direct.add(name)
    return affected_projects(direct, projects), 0


def run_changed(projects: dict[str, dict[str, Any]], base: str | None) -> int:
    changed, code = changed_project_names(projects, base)
    if changed is None:
        return code
    if changed:
        print("\n".join(changed))
    else:
        print("(no affected projects detected)")
    return 0


def run_doctor(projects: dict[str, dict[str, Any]]) -> int:
    print(f"Workspace: {WORKSPACE}")
    root = jj_root()
    errors: list[str] = []
    if root is None:
        errors.append("Jujutsu repository not found")
    else:
        print(f"Jujutsu:  {root}")

    known = set(projects)
    normalized_paths: dict[str, str] = {}
    dependency_graph: dict[str, list[str]] = {}
    for name, spec in projects.items():
        if not isinstance(spec, dict):
            errors.append(f"{name}: project entry must be an object")
            continue
        rel = spec.get("path")
        if not isinstance(rel, str) or not rel:
            errors.append(f"{name}: path must be a non-empty string")
        else:
            path = (WORKSPACE / rel).resolve()
            if path != WORKSPACE.resolve() and WORKSPACE.resolve() not in path.parents:
                errors.append(f"{name}: project path escapes workspace ({rel})")
            elif not path.is_dir():
                errors.append(f"{name}: project directory is missing ({rel})")
            key = rel.replace("\\", "/").rstrip("/").casefold()
            if key in normalized_paths:
                errors.append(f"{name}: path duplicates project {normalized_paths[key]} ({rel})")
            normalized_paths[key] = name

        dependencies = spec.get("depends_on", [])
        if not isinstance(dependencies, list) or not all(isinstance(item, str) for item in dependencies):
            errors.append(f"{name}: depends_on must be an array of project names")
            dependencies = []
        dependency_graph[name] = dependencies
        for dependency in dependencies:
            if dependency not in known:
                errors.append(f"{name}: unknown dependency '{dependency}'")
            elif dependency == name:
                errors.append(f"{name}: project cannot depend on itself")

        tasks = spec.get("tasks", {})
        if not isinstance(tasks, dict):
            errors.append(f"{name}: tasks must be an object")
            tasks = {}
        commands: list[tuple[str, Any]] = [(f"task '{task}'", command) for task, command in tasks.items()]
        ci = spec.get("ci", [])
        if not isinstance(ci, list):
            errors.append(f"{name}: ci must be an array")
            ci = []
        for index, entry in enumerate(ci):
            if not isinstance(entry, dict):
                errors.append(f"{name}: ci entry {index + 1} must be an object")
                continue
            if not isinstance(entry.get("name"), str) or not entry["name"]:
                errors.append(f"{name}: ci entry {index + 1} needs a non-empty name")
            commands.append((f"ci command {index + 1}", entry.get("command")))
        for label, command in commands:
            if not isinstance(command, list) or not command or not all(isinstance(part, str) and part for part in command):
                errors.append(f"{name}: {label} must be a non-empty string argument array")
            elif shutil.which(command[0]) is None:
                errors.append(f"{name}: executable '{command[0]}' for {label} was not found on PATH")

    visiting: set[str] = set()
    visited: set[str] = set()

    def visit(name: str) -> None:
        if name in visiting:
            errors.append(f"dependency cycle includes '{name}'")
            return
        if name in visited:
            return
        visiting.add(name)
        for dependency in dependency_graph.get(name, []):
            if dependency in known:
                visit(dependency)
        visiting.remove(name)
        visited.add(name)

    for name in dependency_graph:
        visit(name)

    configured = 0
    for spec in projects.values():
        if isinstance(spec, dict):
            tasks = spec.get("tasks", {})
            ci = spec.get("ci", [])
            configured += len(tasks) if isinstance(tasks, dict) else 0
            configured += len(ci) if isinstance(ci, list) else 0
    print(f"Projects:  {len(projects)} registered, {configured} task/CI commands")
    if errors:
        print("\nDoctor found issues:")
        for error in errors:
            print(f"  - {error}")
        return 1
    print("Doctor passed: project paths, dependencies, commands, and tool availability look valid.")
    return 0


def check_safety(projects: dict[str, dict[str, Any]]) -> int:
    root = jj_root()
    if root is None:
        print("meingrad: no Jujutsu repository found; safety check cannot inspect repository files", file=sys.stderr)
        return 2
    paths, code = jj_visible_paths(root)
    if paths is None:
        return code
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
    print(f"Safety check passed ({len(paths)} Jujutsu-visible paths checked).")
    return 0


def jj_visible_paths(root: Path) -> tuple[list[str] | None, int]:
    result = subprocess.run(
        jj_command(root, "file", "list", "-r", "@"),
        text=True,
        capture_output=True,
        check=False,
    )
    if result.returncode:
        print("meingrad: jj file list failed", file=sys.stderr)
        if result.stderr:
            print(result.stderr, file=sys.stderr, end="")
        return None, result.returncode
    return [item for item in result.stdout.splitlines() if item], 0


def scan_secrets(root: Path) -> int:
    paths, code = jj_visible_paths(root)
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


def run_ci(
    projects: dict[str, dict[str, Any]],
    selected: str | None,
    changed_only: bool,
    base: str | None,
    security_only: bool,
) -> int:
    root = jj_root()
    if root is None:
        print("meingrad CI: no Jujutsu workspace found", file=sys.stderr)
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
    if selected:
        targets = [selected]
    elif changed_only:
        changed, code = changed_project_names(projects, base)
        if changed is None:
            return code
        targets = changed
        print(f"\nAffected projects: {', '.join(targets) if targets else '(none)'}")
    else:
        targets = list(projects)
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
    changed_parser.add_argument("--base", help="compare the working copy with a Jujutsu revision, e.g. master@origin")
    sub.add_parser("check", help="check Jujutsu-visible files for likely secrets and generated data")
    sub.add_parser("doctor", help="validate the project catalog, dependency graph, and required tools")
    ci_parser = sub.add_parser("ci", help="run local safety, secret, and configured project checks")
    selection = ci_parser.add_mutually_exclusive_group()
    selection.add_argument("--project", help="run configured project checks for one project only")
    selection.add_argument("--changed", action="store_true", help="run checks for changed projects and their dependents")
    ci_parser.add_argument("--base", help="compare the working copy with a Jujutsu revision; requires --changed")
    ci_parser.add_argument("--security-only", action="store_true", help="run repository safety and secret scan only")
    args = parser.parse_args(argv)
    if args.action == "ci" and args.base and not args.changed:
        parser.error("ci --base requires --changed")
    projects = load_projects()
    if args.action == "list":
        return run_list(projects)
    if args.action == "run":
        return run_task(args.project, args.task, projects)
    if args.action == "changed":
        return run_changed(projects, args.base)
    if args.action == "check":
        return check_safety(projects)
    if args.action == "doctor":
        return run_doctor(projects)
    if args.action == "ci":
        return run_ci(projects, args.project, args.changed, args.base, args.security_only)
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
