"""Compare normalized Antithesis assertion properties across implementations."""

from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict
from pathlib import Path
from typing import Any


def load_events(paths: list[Path]) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    for path in paths:
        for line_number, line in enumerate(
            path.read_text(encoding="utf-8").splitlines(), start=1
        ):
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as error:
                raise ValueError(f"{path}:{line_number}: invalid JSON: {error}") from error
            if isinstance(event, dict):
                events.append(event)
    return events


def property_key(assertion: dict[str, Any]) -> str:
    message = str(assertion.get("message", "")).strip().lower()
    details = assertion.get("details") or {}
    if "operation_id" in details and "previous_status" in details:
        return "operation_status_monotonic"
    if "runtime_id" in details and "endpoint" in details:
        return "deployment_runtime_identity"
    if "operation status" in message and "regress" in message:
        return "operation_status_monotonic"
    if "running deployments" in message and "runtime identity" in message:
        return "deployment_runtime_identity"
    return message.replace(" ", "_")


def normalize(events: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    properties: dict[str, dict[str, Any]] = defaultdict(
        lambda: {"hits": 0, "failures": 0, "details": []}
    )
    for event in events:
        assertion = event.get("antithesis_assert")
        if not isinstance(assertion, dict):
            continue
        key = property_key(assertion)
        condition = bool(assertion.get("condition", False))
        entry = properties[key]
        entry["hits"] += 1
        entry["failures"] += int(not condition)
        details = assertion.get("details")
        if isinstance(details, dict):
            comparable = {
                name: details[name]
                for name in (
                    "operation_kind",
                    "previous_status",
                    "next_status",
                    "runtime_id",
                    "endpoint",
                )
                if name in details
            }
            if comparable and comparable not in entry["details"]:
                entry["details"].append(comparable)
    return dict(properties)


def compare(reference: dict[str, dict[str, Any]], candidate: dict[str, dict[str, Any]]) -> list[str]:
    errors: list[str] = []
    reference_keys = set(reference)
    candidate_keys = set(candidate)
    for key in sorted(reference_keys - candidate_keys):
        errors.append(f"candidate is missing property: {key}")
    for key in sorted(reference_keys & candidate_keys):
        if reference[key]["failures"] != candidate[key]["failures"]:
            errors.append(
                f"{key}: failure count differs ({reference[key]['failures']} vs "
                f"{candidate[key]['failures']})"
            )
        for field in ("previous_status", "next_status", "runtime_id", "endpoint"):
            if field == "runtime_id":
                reference_values = {
                    True for item in reference[key]["details"] if field in item
                }
                candidate_values = {
                    True for item in candidate[key]["details"] if field in item
                }
            else:
                reference_values = {
                    item[field] for item in reference[key]["details"] if field in item
                }
                candidate_values = {
                    item[field] for item in candidate[key]["details"] if field in item
                }
            if reference_values and not reference_values <= candidate_values:
                errors.append(
                    f"{key}: candidate lacks {field} evidence "
                    f"{sorted(reference_values - candidate_values)}"
                )
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--reference",
        type=Path,
        action="append",
        required=True,
        help="reference JSONL file; repeat for multiple implementations",
    )
    parser.add_argument("candidate", type=Path, help="candidate JSONL file")
    args = parser.parse_args()
    try:
        reference = normalize(load_events(args.reference))
        candidate = normalize(load_events([args.candidate]))
        errors = compare(reference, candidate)
    except (OSError, ValueError) as error:
        print(f"assertion differential failed: {error}", file=sys.stderr)
        return 2
    print(json.dumps({"reference": reference, "candidate": candidate}, indent=2, sort_keys=True))
    if errors:
        print("\nAssertion differential failures:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1
    print("Assertion differential: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
