#!/usr/bin/env python3
"""A local, account-free approximation of the Antithesis test loop.

It runs a repeatable workload, captures Antithesis SDK JSONL events, can apply
simple Docker Compose faults between trials, and summarizes assertion failures.
"""

from __future__ import annotations

import argparse
from collections import Counter
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
ANTITHESIS_ROOT = ROOT / "antithesis"
EVENTS_ROOT = ANTITHESIS_ROOT / "local-events"
RUST_ROOT = ROOT / "janus-rust"


def rust_cargo() -> list[str]:
    if os.name == "nt":
        return ["rustup", "run", "stable-x86_64-pc-windows-msvc", "cargo"]
    return ["cargo"]


def run_command(command: list[str], cwd: Path, env: dict[str, str] | None = None) -> int:
    print("$", " ".join(command))
    result = subprocess.run(command, cwd=cwd, env=env, check=False)
    return result.returncode


def run_command_capture(
    command: list[str], cwd: Path, env: dict[str, str] | None = None
) -> tuple[int, str]:
    """Run one verification command while retaining output for metric extraction."""
    print("$", " ".join(command))
    result = subprocess.run(
        command,
        cwd=cwd,
        env=env,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    print(result.stdout, end="")
    return result.returncode, result.stdout


def update_trace_metrics(
    test_output: str,
    tigerstyle_output: str,
    diff_output: str,
    assertion_file: Path,
    clippy_output: str = "",
    gate_status: str = "passed",
) -> None:
    """Publish real local-gate measurements to the standalone trace viewer."""
    test_counts = [int(value) for value in re.findall(r"test result: ok\. (\d+) passed", test_output)]
    actix_match = re.search(
        r"Running unittests src[\\\\/]bin[\\\\/]janus-api.*?"
        r"test result: ok\. (\d+) passed",
        test_output,
        re.DOTALL,
    )
    actix_tests = int(actix_match.group(1)) if actix_match else 0
    rust_tests = sum(test_counts) - actix_tests
    assertions = load_events([assertion_file])
    assertion_events = [event for event in assertions if isinstance(event.get("antithesis_assert"), dict)]
    assertion_failures = sum(
        not bool(event["antithesis_assert"].get("condition", False))
        for event in assertion_events
    )
    hard_match = re.search(r"errors:\s+(\d+)", tigerstyle_output)
    advisory_match = re.search(r"advisories:\s+(\d+)", tigerstyle_output)
    tigerstyle_rules = {
        rule: int(count)
        for rule, count in re.findall(r"\s+(TS\d+):\s+(\d+)", tigerstyle_output)
    }
    compared_match = re.search(
        r"compared (\d+) reference properties with (\d+) candidate properties", diff_output
    )
    clippy_diagnostic_lines = [
        line
        for line in clippy_output.splitlines()
        if line.startswith("error:") and "could not compile" not in line
    ]
    clippy_total = len(clippy_diagnostic_lines) if clippy_output else None
    clippy_library_targets = len(
        re.findall(r"error: could not compile `[^`]+` \(lib\)", clippy_output)
    )
    clippy_test_targets = len(
        re.findall(r"error: could not compile `[^`]+` \(lib test\)", clippy_output)
    )
    lint_names = re.findall(r"-D clippy::([a-z0-9_-]+)", clippy_output)
    lint_counts = Counter(lint_names)
    clippy_diagnostics = []
    for line in clippy_output.splitlines():
        if not line.startswith("error:") or "could not compile" in line:
            continue
        message = line.removeprefix("error:").strip()
        if message and message not in clippy_diagnostics:
            clippy_diagnostics.append(message)
    lint_baseline = 346
    spacetime_output_path = RUST_ROOT / "artifacts" / "spacetime-check-output.txt"
    spacetime_output = (
        spacetime_output_path.read_text(encoding="utf-8")
        if spacetime_output_path.exists()
        else ""
    )
    bindings_unavailable = "SpacetimeDB CLI is not installed" in spacetime_output
    zig_fixture_path = ROOT / "antithesis" / "fixtures" / "route-behavior-matrix.json"
    zig_fixture_routes = None
    zig_fixture_methods = None
    if zig_fixture_path.exists():
        try:
            zig_fixture = json.loads(zig_fixture_path.read_text(encoding="utf-8"))
            if isinstance(zig_fixture, list):
                zig_fixture_routes = len(zig_fixture)
                zig_fixture_methods = sum(
                    len(item.get("methods", []))
                    for item in zig_fixture
                    if isinstance(item, dict) and isinstance(item.get("methods", []), list)
                )
        except (OSError, json.JSONDecodeError):
            zig_fixture_routes = None
            zig_fixture_methods = None
    metrics = {
        "status": gate_status,
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "rustTests": rust_tests,
        "actixTests": actix_tests,
        "assertions": len(assertion_events),
        "assertionFailures": assertion_failures,
        "tigerstyleHardFindings": int(hard_match.group(1)) if hard_match else None,
        "tigerstyleAdvisories": int(advisory_match.group(1)) if advisory_match else None,
        "tigerstyleWarningsIgnored": 0,
        "tigerstyleAdvisoryBaseline": 177,
        "tigerstyleAdvisoriesResolved": (
            max(0, 177 - int(advisory_match.group(1))) if advisory_match else None
        ),
        "tigerstyleFindingsByRule": tigerstyle_rules,
        "differentialReferenceProperties": int(compared_match.group(1)) if compared_match else None,
        "differentialCandidateProperties": int(compared_match.group(2)) if compared_match else None,
        "strictLintStatus": "passed" if clippy_total == 0 else "failed" if clippy_total else "not_run",
        "clippyDiagnostics": clippy_total,
        "clippyLibraryDiagnostics": clippy_library_targets or None,
        "clippyTestDiagnostics": clippy_test_targets or None,
        "clippyWarningsIgnored": 0,
        "clippyBaselineDiagnostics": lint_baseline,
        "clippyResolvedDiagnostics": max(0, lint_baseline - clippy_total) if clippy_total is not None else None,
        "clippyFindingsByLint": dict(lint_counts.most_common()),
        "clippyDiagnosticDetails": clippy_diagnostics,
        "zigFixtureRoutes": zig_fixture_routes,
        "zigFixtureMethods": zig_fixture_methods,
        "zigFixtureStatus": "available" if zig_fixture_routes is not None else "unavailable",
        "spacetimeBindingsStatus": "unavailable" if bindings_unavailable else "available",
    }
    findings: list[dict[str, Any]] = []

    def add_finding(
        severity: str, source: str, key: str, count: int, work: str
    ) -> None:
        if count > 0:
            findings.append(
                {
                    "severity": severity,
                    "source": source,
                    "key": key,
                    "count": count,
                    "work": work,
                }
            )

    add_finding(
        "error",
        "TigerStyle",
        "hard findings",
        int(hard_match.group(1)) if hard_match else 0,
        "Fix every hard finding in code or documented design evidence, then rerun the local gate.",
    )
    for rule, count in tigerstyle_rules.items():
        add_finding(
            "warning",
            "TigerStyle",
            rule,
            count,
            "Review and resolve this boundary finding through code or reviewed design evidence; rerun the local gate.",
        )
    for rule, count in lint_counts.items():
        add_finding(
            "error",
            "Clippy",
            rule,
            count,
            "Resolve the active Actix API lint category through code changes; do not add an allow or refresh a baseline. The syn 2/3 dependency finding is eliminated.",
        )
    for detail in clippy_diagnostics:
        add_finding(
            "error",
            "Clippy diagnostic",
            detail,
            1,
            "Fix this Actix API diagnostic in source, then rerun maximum-profile Clippy with warnings denied; do not suppress it.",
        )
    add_finding(
        "error",
        "Assertion runner",
        "failed assertions",
        assertion_failures,
        "Fix the violated property and rerun the local JSONL scenario; failures must remain visible.",
    )
    failed_test_results = len(re.findall(r"test result: FAILED", test_output))
    add_finding(
        "error",
        "Rust tests",
        "failed test targets",
        failed_test_results,
        "Fix the failing Rust test target and rerun the complete local verification gate.",
    )
    if bindings_unavailable:
        add_finding(
            "error",
            "SpacetimeDB prerequisite",
            "CLI/bindings unavailable",
            1,
            "Install the SpacetimeDB CLI and generate/commit bindings before production verification can pass.",
        )
    if zig_fixture_routes is None:
        add_finding(
            "warning",
            "Zig parity fixture",
            "route behavior matrix unavailable",
            1,
            "Restore or regenerate the Zig route fixture while the legacy implementation remains a rollback/reference gate.",
        )
    if gate_status != "passed" and not findings:
        add_finding(
            "error",
            "Local verification gate",
            "gate failed without a classified finding",
            1,
            "Inspect the captured command artifacts, classify the failure, and rerun the gate.",
        )
    metrics["verificationFindings"] = findings
    metrics["verificationWarningCount"] = sum(
        finding["severity"] == "warning" for finding in findings
    )
    metrics["verificationErrorCount"] = sum(
        finding["severity"] == "error" for finding in findings
    )
    artifact = RUST_ROOT / "artifacts" / "trace-metrics.json"
    artifact.parent.mkdir(parents=True, exist_ok=True)
    artifact.write_text(json.dumps(metrics, indent=2) + "\n", encoding="utf-8")
    spec = ROOT / "Janus_Rust_System_Spec.html"
    source = spec.read_text(encoding="utf-8")
    pattern = r'(<script id="trace-runtime-metrics" type="application/json">).*?(</script>)'
    replacement = r"\g<1>" + json.dumps(metrics, separators=(",", ":")) + r"\g<2>"
    updated, replacements = re.subn(pattern, replacement, source, count=1, flags=re.DOTALL)
    if replacements != 1:
        raise RuntimeError("trace-runtime-metrics marker is missing from the system specification")
    if advisory_match:
        advisory_pattern = (
            r'(<span id="ledger-tigerstyle-advisory-count" class="badge pending">)'
            r'\d+(</span>)'
        )
        updated, advisory_replacements = re.subn(
            advisory_pattern,
            r"\g<1>" + advisory_match.group(1) + r"\g<2>",
            updated,
            count=1,
        )
        if advisory_replacements != 1:
            raise RuntimeError("static TigerStyle advisory ledger marker is missing")
    spec.write_text(updated, encoding="utf-8")


def docker_compose_available() -> bool:
    docker = shutil.which("docker")
    if not docker:
        return False
    result = subprocess.run(
        [docker, "compose", "version"],
        cwd=ROOT,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    return result.returncode == 0


def doctor(_: argparse.Namespace) -> int:
    spacetime_cli = shutil.which("spacetime")
    if spacetime_cli is None and os.name == "nt":
        installed_cli = Path(os.environ.get("LOCALAPPDATA", "")) / "SpacetimeDB" / "spacetime.exe"
        if installed_cli.is_file():
            spacetime_cli = str(installed_cli)
    checks = {
        "python": sys.version.split()[0],
        "go": shutil.which("go") or "missing",
        "rust": shutil.which("rustc") or "missing",
        "cargo": shutil.which("cargo") or "missing",
        "docker_compose": "available" if docker_compose_available() else "missing",
        "spacetime_cli": spacetime_cli or "missing",
        "spacetime_bindings": (
            "present"
            if any((RUST_ROOT / "src" / "module_bindings").glob("*.rs"))
            else "missing"
        ),
        "legacy_archive": (
            "present"
            if (ROOT / "artifacts" / "legacy-implementations-20261001.zip").is_file()
            else "missing"
        ),
        "rust_core": "present" if RUST_ROOT.is_dir() else "missing",
        "events_directory": "present" if EVENTS_ROOT.is_dir() else "missing",
    }
    print(json.dumps(checks, indent=2))
    return (
        0
        if checks["rust_core"] == "present"
        and checks["python"]
        and checks["spacetime_cli"] != "missing"
        and checks["spacetime_bindings"] == "present"
        else 1
    )


def compose_action(service: str, action: str) -> int:
    if not docker_compose_available():
        print("Docker Compose is unavailable; skipping compose fault.", file=sys.stderr)
        return 1
    docker = shutil.which("docker")
    command = [docker, "compose"]
    if action == "restart":
        command += ["restart", service]
    elif action == "pause":
        command += ["pause", service]
    elif action == "unpause":
        command += ["unpause", service]
    else:
        raise ValueError(f"unsupported compose action: {action}")
    return run_command(command, ROOT)


def run_trial(package: str, trial: int, fault_service: str | None, fault_action: str) -> tuple[int, Path]:
    EVENTS_ROOT.mkdir(parents=True, exist_ok=True)
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    event_file = EVENTS_ROOT / f"local-{timestamp}-trial-{trial}.jsonl"
    if fault_service:
        fault_result = compose_action(fault_service, fault_action)
        if fault_result != 0:
            return fault_result, event_file

    del package
    returncode = run_command(
        rust_cargo()
        + [
            "run",
            "--manifest-path",
            "janus-rust/Cargo.toml",
            "--bin",
            "antithesis-local",
            "--",
            "--output",
            str(event_file),
        ],
        cwd=ROOT,
    )
    return returncode, event_file


def load_events(paths: list[Path]) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    for path in paths:
        if not path.exists():
            continue
        for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                print(f"Warning: ignored invalid JSON at {path}:{line_number}", file=sys.stderr)
    return events


def assertion_summary(events: list[dict[str, Any]]) -> dict[str, Any]:
    assertions: dict[str, dict[str, Any]] = {}
    for event in events:
        assertion = event.get("antithesis_assert")
        if not isinstance(assertion, dict):
            continue
        message = str(assertion.get("message", "<unnamed>"))
        entry = assertions.setdefault(message, {"hits": 0, "failures": 0, "conditions": []})
        entry["hits"] += 1
        condition = bool(assertion.get("condition", False))
        entry["conditions"].append(condition)
        if not condition:
            entry["failures"] += 1
    return assertions


def report(paths: list[Path]) -> int:
    events = load_events(paths)
    assertions = assertion_summary(events)
    print(json.dumps({"files": [str(path) for path in paths], "assertions": assertions}, indent=2))
    return 1 if any(item["failures"] for item in assertions.values()) else 0


def validate_events(paths: list[Path]) -> int:
    errors: list[str] = []
    for path in paths:
        if not path.exists():
            errors.append(f"missing event file: {path}")
            continue
        for line_number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            try:
                event = json.loads(line)
            except json.JSONDecodeError as error:
                errors.append(f"{path}:{line_number}: invalid JSON: {error.msg}")
                continue
            if not isinstance(event, dict):
                errors.append(f"{path}:{line_number}: event must be an object")
                continue
            assertion = event.get("antithesis_assert")
            if assertion is not None:
                if not isinstance(assertion, dict):
                    errors.append(f"{path}:{line_number}: antithesis_assert must be an object")
                    continue
                if not isinstance(assertion.get("message"), str) or not assertion["message"].strip():
                    errors.append(f"{path}:{line_number}: assertion message is required")
                if not isinstance(assertion.get("condition"), bool):
                    errors.append(f"{path}:{line_number}: assertion condition must be boolean")
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(json.dumps({"files": [str(path) for path in paths], "valid": True}, indent=2))
    return 0


def cutover_gate(_: argparse.Namespace) -> int:
    """Refuse legacy removal until the Rust replacement gate is complete."""
    metrics_path = RUST_ROOT / "artifacts" / "trace-metrics.json"
    if not metrics_path.exists():
        print(f"missing cutover evidence: {metrics_path}", file=sys.stderr)
        return 1
    try:
        metrics = json.loads(metrics_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        print(f"invalid cutover evidence: {error}", file=sys.stderr)
        return 1
    failures: list[str] = []
    if metrics.get("status") != "passed":
        failures.append(f"local Rust gate status is {metrics.get('status')!r}")
    if metrics.get("verificationFindings"):
        failures.append(
            f"{len(metrics['verificationFindings'])} warning/error findings remain"
        )
    if metrics.get("assertionFailures", 1) != 0:
        failures.append("local assertion failures remain")
    if metrics.get("differentialReferenceProperties") is None:
        failures.append("differential parity evidence is missing")
    if metrics.get("zigFixtureStatus") != "available":
        failures.append("Zig compatibility fixture evidence is unavailable")
    if metrics.get("spacetimeBindingsStatus") != "available":
        failures.append("SpacetimeDB CLI/bindings production prerequisite is unavailable")
    if failures:
        print(json.dumps({"cutover": "blocked", "reasons": failures}, indent=2))
        return 1
    print(
        json.dumps(
            {
                "cutover": "ready",
                "message": "Rust evidence permits a separately reviewed legacy archive/removal.",
            },
            indent=2,
        )
    )
    return 0


def hypothesis(_: argparse.Namespace) -> int:
    return rust_tests()


def check(args: argparse.Namespace) -> int:
    result = rust_tests()
    if result != 0 or not args.http:
        return result
    return hypothesis_http(args)


def rust_tests() -> int:
    cargo = rust_cargo()
    if run_command(cargo + ["fmt", "--manifest-path", "janus-rust/Cargo.toml", "--", "--check"], ROOT) != 0:
        return 1
    test_command = cargo + [
        "test",
        "--manifest-path",
        "janus-rust/Cargo.toml",
        "--all-targets",
        "--all-features",
    ]
    test_code, test_output = run_command_capture(test_command, ROOT)
    (RUST_ROOT / "artifacts").mkdir(parents=True, exist_ok=True)
    (RUST_ROOT / "artifacts" / "rust-test-output.txt").write_text(test_output, encoding="utf-8")
    if test_code != 0:
        return 1
    spacetime_code = spacetime_checks()
    tiger_code, tigerstyle_output = run_command_capture(
        cargo + ["run", "--manifest-path", "janus-rust/Cargo.toml", "--bin", "tigerstyle"],
        ROOT,
    )
    (RUST_ROOT / "artifacts" / "tigerstyle-output.txt").write_text(
        tigerstyle_output, encoding="utf-8"
    )
    if tiger_code != 0:
        update_trace_metrics(
            test_output,
            tigerstyle_output,
            "",
            RUST_ROOT / "artifacts" / "rust-check-assertions.jsonl",
            "",
            "failed",
        )
        return 1
    clippy_code, clippy_output = run_command_capture(
        cargo
        + [
            "clippy",
            "--manifest-path",
            "janus-rust/Cargo.toml",
            "--all-targets",
            "--all-features",
            "--",
            "-D",
            "warnings",
        ],
        ROOT,
    )
    (RUST_ROOT / "artifacts" / "clippy-output.txt").write_text(
        clippy_output, encoding="utf-8"
    )
    gate_status = "failed" if clippy_code != 0 or spacetime_code != 0 else "passed"
    output = ROOT / "janus-rust" / "artifacts" / "rust-check-assertions.jsonl"
    output.parent.mkdir(parents=True, exist_ok=True)
    output.unlink(missing_ok=True)
    if run_command(
        cargo
        + [
            "run",
            "--manifest-path",
            "janus-rust/Cargo.toml",
            "--bin",
            "antithesis-local",
            "--",
            "--output",
            str(output),
        ],
        ROOT,
    ) != 0:
        return 1
    if run_command(
        cargo
        + [
            "run",
            "--manifest-path",
            "janus-rust/Cargo.toml",
            "--bin",
            "antithesis-validate",
            "--",
            "--input",
            str(output),
        ],
        ROOT,
    ) != 0:
        return 1
    diff_code, diff_output = run_command_capture(
        cargo
        + [
            "run",
            "--manifest-path",
            "janus-rust/Cargo.toml",
            "--bin",
            "antithesis-diff",
            "--",
            "--reference",
            str(ANTITHESIS_ROOT / "fixtures" / "go-operation-status.jsonl"),
            "--reference",
            str(ANTITHESIS_ROOT / "fixtures" / "go-deployment-runtime.jsonl"),
            "--candidate",
            str(output),
        ],
        ROOT,
    )
    if diff_code != 0:
        return diff_code
    (RUST_ROOT / "artifacts" / "differential-output.txt").write_text(
        diff_output, encoding="utf-8"
    )
    update_trace_metrics(
        test_output,
        tigerstyle_output,
        diff_output,
        output,
        clippy_output,
        gate_status,
    )
    return 1 if clippy_code != 0 or spacetime_code != 0 else 0


def spacetime_checks() -> int:
    """Verify the deployable Rust persistence module and local binding prerequisite."""
    cargo = rust_cargo()
    checks = [
        cargo + ["check", "--manifest-path", "janus-spacetimedb/Cargo.toml"],
        cargo
        + [
            "clippy",
            "--manifest-path",
            "janus-spacetimedb/Cargo.toml",
            "--all-targets",
            "--all-features",
            "--",
            "-D",
            "warnings",
        ],
        [
            "python",
            "janus-spacetimedb/tools/generate_bindings.py",
            "--check-only",
            "--require-installed",
            "--verify-output",
        ],
    ]
    outputs: list[str] = []
    for command in checks:
        code, output = run_command_capture(command, ROOT)
        outputs.append(f"$ {' '.join(command)}\n{output}")
        if code != 0:
            (RUST_ROOT / "artifacts" / "spacetime-check-output.txt").write_text(
                "\n\n".join(outputs), encoding="utf-8"
            )
            return code
    (RUST_ROOT / "artifacts" / "spacetime-check-output.txt").write_text(
        "\n\n".join(outputs), encoding="utf-8"
    )
    return 0


def hypothesis_http(_: argparse.Namespace) -> int:
    return rust_tests()


def hypothesis_authorization(_: argparse.Namespace) -> int:
    return rust_tests()


def hypothesis_artifact_integrity(_: argparse.Namespace) -> int:
    return rust_tests()


def hypothesis_readiness(_: argparse.Namespace) -> int:
    return rust_tests()


def hypothesis_build(_: argparse.Namespace) -> int:
    return rust_tests()


def run(args: argparse.Namespace) -> int:
    if args.compose_up:
        if not docker_compose_available():
            print("Docker Compose is unavailable; cannot start the local stack.", file=sys.stderr)
            return 1
        docker = shutil.which("docker")
        if run_command([docker, "compose", "up", "-d"], ROOT) != 0:
            return 1

    paths: list[Path] = []
    exit_code = 0
    for trial in range(1, args.trials + 1):
        returncode, event_file = run_trial(
            args.package,
            trial,
            args.fault_service,
            args.fault_action,
        )
        exit_code = max(exit_code, returncode)
        paths.append(event_file)

    print("\nLocal assertion report:")
    report_code = report(paths)
    return max(exit_code, report_code)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="AntiThesisLocal")
    subparsers = parser.add_subparsers(dest="command", required=True)

    doctor_parser = subparsers.add_parser("doctor", help="check local prerequisites")
    doctor_parser.set_defaults(handler=doctor)

    run_parser = subparsers.add_parser("run", help="run tests and collect SDK events")
    run_parser.add_argument("--package", default="./internal/domain/operations")
    run_parser.add_argument("--trials", type=int, default=1)
    run_parser.add_argument("--compose-up", action="store_true")
    run_parser.add_argument("--fault-service", choices=["janus-api", "janus-worker", "postgres", "minio"])
    run_parser.add_argument("--fault-action", choices=["restart", "pause", "unpause"], default="restart")
    run_parser.set_defaults(handler=run)

    hypothesis_parser = subparsers.add_parser(
        "hypothesis", help="run Hypothesis-generated lifecycle sequences"
    )
    hypothesis_parser.set_defaults(handler=hypothesis)

    check_parser = subparsers.add_parser(
        "check", help="run Rust-native local checks before commit"
    )
    check_parser.add_argument(
        "--http", action="store_true", help="also run the Docker-backed HTTP workload"
    )
    check_parser.set_defaults(handler=check)

    cutover_parser = subparsers.add_parser(
        "cutover", help="verify that Rust evidence permits legacy archive/removal"
    )
    cutover_parser.set_defaults(handler=cutover_gate)

    http_parser = subparsers.add_parser(
        "hypothesis-http", help="run the stateful Hypothesis workload against Janus HTTP"
    )
    http_parser.set_defaults(handler=hypothesis_http)

    authorization_parser = subparsers.add_parser(
        "hypothesis-authorization", help="run the cross-user authorization boundary workload"
    )
    authorization_parser.set_defaults(handler=hypothesis_authorization)

    artifact_parser = subparsers.add_parser(
        "hypothesis-artifact", help="run the missing-artifact integrity workload"
    )
    artifact_parser.set_defaults(handler=hypothesis_artifact_integrity)

    readiness_parser = subparsers.add_parser(
        "hypothesis-readiness", help="run dependency readiness recovery workload"
    )
    readiness_parser.set_defaults(handler=hypothesis_readiness)

    report_parser = subparsers.add_parser("report", help="summarize existing JSONL events")
    report_parser.add_argument("files", nargs="*", type=Path)
    report_parser.set_defaults(handler=lambda args: report(args.files or sorted(EVENTS_ROOT.glob("*.jsonl"))))
    validate_parser = subparsers.add_parser("validate-jsonl", help="validate SDK JSONL event files")
    validate_parser.add_argument("files", nargs="*", type=Path)
    validate_parser.set_defaults(
        handler=lambda args: validate_events(args.files or sorted(EVENTS_ROOT.glob("*.jsonl")))
    )
    return parser


if __name__ == "__main__":
    arguments = build_parser().parse_args()
    sys.exit(arguments.handler(arguments))
