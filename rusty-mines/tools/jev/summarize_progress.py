"""Evidence-linked action report. Advisory judgments never close acceptance gates."""

import argparse
import collections
import hashlib
import json
import math
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def load(path, default):
    return json.loads(path.read_text(encoding="utf-8")) if path.is_file() else default


def dump(path, value):
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False), encoding="utf-8")


def validate_actions(actions):
    ids = {a["id"] for a in actions}
    if len(ids) != len(actions):
        raise ValueError("Duplicate action ID")
    for a in actions:
        for field in (
            "category",
            "status",
            "next_step",
            "acceptance_criteria",
            "evidence_refs",
            "files",
            "dependencies",
            "priority",
        ):
            if field not in a or (
                field in ("next_step", "acceptance_criteria", "evidence_refs")
                and not a[field]
            ):
                raise ValueError(f"{a['id']}: missing {field}")
        if any(d not in ids for d in a["dependencies"]):
            raise ValueError(f"{a['id']}: unknown dependency")

    def visit(id, active):
        if id in active:
            raise ValueError("Cyclic action dependency")
        for dep in next(a for a in actions if a["id"] == id)["dependencies"]:
            visit(dep, active | {id})

    for id in ids:
        visit(id, set())


def valid_response(request, response):
    answers = response.get("answers", {})
    if not isinstance(answers, dict) or set(answers) != set(request["questions"]):
        return False

    def probability(value):
        return (
            isinstance(value, (int, float))
            and not isinstance(value, bool)
            and math.isfinite(value)
            and 0 <= value <= 1
        )

    for id, answer in answers.items():
        options = set(request["questions"][id]["criteria"])
        if (
            not isinstance(answer, dict)
            or answer.get("type") != "choice"
            or answer.get("choice") not in options
            or not probability(answer.get("confidence"))
        ):
            return False
        distribution = answer.get("probabilities", {})
        if (
            not isinstance(distribution, dict)
            or set(distribution) != options
            or not all(probability(p) for p in distribution.values())
            or abs(sum(distribution.values()) - 1) > 0.02
        ):
            return False
    return True


def triage_refs_exist(out, root, refs):
    if not isinstance(refs, list) or not refs:
        return False
    for ref in refs:
        if not isinstance(ref, str):
            return False
        name = ref.split("#", 1)[0]
        if not name:
            return False
        candidates = [(out / name).resolve(), (root / name).resolve()]
        if not any(
            p.is_relative_to(root.resolve()) and p.is_file() for p in candidates
        ):
            return False
    return True


def summarize(out, root=ROOT, baseline=None):
    manifest = load(out / "manifest.json", {})
    if not manifest.get("source_sha256"):
        raise ValueError(
            "Snapshot manifest is required; historical manifests are not reconstructed"
        )
    stale = set(manifest.get("changed_during_review", []))
    stale.update(
        p
        for p, h in manifest["source_sha256"].items()
        if not (root / p).is_file()
        or hashlib.sha256((root / p).read_bytes()).hexdigest() != h
    )
    records = load(out / "validation.json", [])
    triage = load(out / "triage.json", {})
    checks, actions, usage = [], [], collections.Counter()

    def add(
        id, category, step, criteria, files, refs, priority="P1", blocker=None, deps=()
    ):
        a = dict(
            id=id,
            category=category,
            status="blocked" if blocker else "ready",
            priority=priority,
            files=files,
            next_step=step,
            acceptance_criteria=criteria,
            evidence_refs=refs,
            dependencies=list(deps),
            blocker=blocker,
        )
        actions.append(a)
        return a

    add(
        "EVIDENCE-FRESHNESS",
        "stale_evidence" if stale else "missing_execution",
        "Capture an unchanged source snapshot and run the complete local validation suite.",
        ["All required commands pass against an unchanged snapshot."],
        sorted(stale),
        ["manifest.json", "validation.json"],
        "P0",
    )
    from progress_review import validation_commands

    passed = {
        tuple(r["command"])
        for r in records
        if r.get("exit_code") == 0
        and r.get("fresh")
        and (out / r.get("log", "__missing__")).is_file()
    }
    for i, record in enumerate(records):
        if record.get("exit_code") != 0:
            add(
                "LOCAL-GATE-" + str(i + 1),
                "missing_execution",
                "Investigate and rerun: " + " ".join(record["command"]),
                [
                    "Command exits zero against an unchanged snapshot; preserve the failing and passing logs."
                ],
                [],
                ["validation.json#/" + str(i), record["log"]],
                "P0",
            )
    if not stale and all(tuple(c) in passed for c in validation_commands()):
        actions[0]["status"] = "verified"
    if manifest.get("preparation_error"):
        add(
            "EVIDENCE-COVERAGE",
            "missing_evidence",
            "Fix evidence preparation: " + manifest["preparation_error"],
            [
                "Required functions are found, ranges are inspected and every batch fits the explicit context limit before upload."
            ],
            [],
            ["manifest.json#/preparation_error"],
            "P0",
        )
    for batch, failure in manifest.get("api_failures", {}).items():
        add(
            "REVIEW-BATCH-" + batch,
            "missing_execution",
            "Investigate the failed batch and explicitly retry only if authorized: "
            + batch,
            [
                "A typed valid response is retained, or the service failure remains explicitly unassessed."
            ],
            [],
            ["manifest.json#/api_failures/" + batch],
            "P1",
            "Explicit --live --retry-failed required for another billed request",
        )
    for request_path in sorted(out.glob("*.request.json")):
        group = request_path.name.removesuffix(".request.json")
        request = load(request_path, {})
        coverage = load(out / f"{group}.coverage.json", {})
        response = load(out / f"{group}.response.json", {})
        answers = response.get("answers", {})
        valid = valid_response(request, response)
        if response and not valid:
            answers = {}
        usage.update(response.get("usage", {}) if valid else {})
        files = [r["path"] for r in request["state"]["manifest"]]
        affected = bool(stale.intersection(files))
        for id, question in request["questions"].items():
            answer = answers.get(id, {})
            verdict = answer.get("choice", "not_assessed")
            execution = group == "acceptance_records"
            manual = triage.get(id, {})
            if manual and not triage_refs_exist(out, root, manual.get("evidence_refs")):
                raise ValueError(
                    f"{id}: independent triage requires existing local evidence files"
                )
            category = (
                "stale_evidence"
                if affected
                else "missing_evidence"
                if coverage.get("missing")
                else "missing_execution"
                if execution
                else "reviewer_disagreement"
                if verdict == "contradicts"
                else "missing_test"
                if group == "local_test_design" and verdict == "insufficient_evidence"
                else "missing_evidence"
                if verdict in ("not_assessed", "insufficient_evidence")
                else "source_supported"
            )
            if (
                manual.get("category")
                in ("confirmed_defect", "out_of_scope", "reviewer_disagreement")
                and triage_refs_exist(out, root, manual.get("evidence_refs"))
                and not affected
            ):
                category = manual["category"]
            refs = [
                request_path.name + "#/questions/" + id,
                f"{group}.coverage.json",
                "manifest.json",
            ]
            if answer:
                refs.append(f"{group}.response.json#/answers/{id}")
            refs += manual.get("evidence_refs", [])
            checks.append(
                dict(
                    id=id,
                    batch=group,
                    verdict=verdict,
                    confidence=answer.get("confidence"),
                    category=category,
                    stale=affected,
                    evidence_refs=refs,
                )
            )
            if category == "source_supported":
                continue
            if execution:
                blocker = (
                    "Explicit disposable backend/admin authorization required"
                    if any(s in id for s in ("live", "rollback", "restart", "race"))
                    else "Renewed graphical authorization required"
                    if any(s in id for s in ("two_client", "m7_acceptance"))
                    else None
                )
                step = (
                    "Record a scoped execution test for: "
                    + question["instructions"]["claim"]
                )
                criteria = [
                    "Record source hashes, exact command/scenario and actual outcome; test definitions or Jev support are insufficient."
                ]
            else:
                blocker = None
                step = (
                    "Inspect the selected implementation and dependencies for: "
                    + question["instructions"]["claim"]
                )
                criteria = [
                    "Reproduce a defect or document missing evidence with exact source/test references before changing code."
                ]
            a = add(
                "CHECK-" + id,
                category,
                manual.get("next_step", step),
                manual.get("acceptance_criteria", criteria),
                files,
                refs,
                "P0" if execution else "P1",
                blocker,
            )
            if category == "stale_evidence":
                a["dependencies"] = ["EVIDENCE-FRESHNESS"]
                a["status"] = "planned"
            if category == "out_of_scope":
                a["status"] = "out_of_scope"
    # Graphical demo execution follows authority, persistence and codec/queue gates.
    ids = {a["id"] for a in actions}
    for a in actions:
        if a["id"] in ("CHECK-current_two_client_pass", "CHECK-current_m7_acceptance"):
            for gate in (
                "current_live_mutation_pass",
                "current_rollback_pass",
                "current_restart_pass",
                "current_race_pass",
                "current_component_review",
                "current_backpressure_pass",
            ):
                dep = "CHECK-" + gate
                if dep in ids and dep not in a["dependencies"]:
                    a["dependencies"].append(dep)
    # No AI verdict or manually asserted status is sufficient to close an acceptance action.
    for a in actions:
        if any(
            next(d for d in actions if d["id"] == dep)["status"] != "verified"
            for dep in a["dependencies"]
        ):
            if a["status"] == "ready":
                a["status"] = "planned"
    validate_actions(actions)
    baseline = (
        Path(baseline)
        if baseline
        else Path(manifest["baseline"])
        if manifest.get("baseline")
        else None
    )
    old_actions = (
        {a["id"]: a for a in load(baseline / "actions.json", {}).get("actions", [])}
        if baseline
        else {}
    )
    old_checks = (
        {c["id"]: c for c in load(baseline / "results.json", {}).get("checks", [])}
        if baseline
        else {}
    )
    delta = {
        "new": [a["id"] for a in actions if a["id"] not in old_actions],
        "unchanged": [
            a["id"]
            for a in actions
            if a["id"] in old_actions and a["status"] == old_actions[a["id"]]["status"]
        ],
        "status_changes": [
            {
                "id": a["id"],
                "before": old_actions[a["id"]]["status"],
                "after": a["status"],
            }
            for a in actions
            if a["id"] in old_actions and a["status"] != old_actions[a["id"]]["status"]
        ],
        "not_reassessed": sorted(set(old_actions) - {a["id"] for a in actions}),
        "verdict_changes": [
            {
                "id": c["id"],
                "before": old_checks[c["id"]].get(
                    "verdict", old_checks[c["id"]].get("choice")
                ),
                "after": c["verdict"],
            }
            for c in checks
            if c["id"] in old_checks
            and c["verdict"]
            != old_checks[c["id"]].get("verdict", old_checks[c["id"]].get("choice"))
        ],
    }
    ready = sorted(
        (a for a in actions if a["status"] == "ready"),
        key=lambda a: (
            a["priority"],
            0
            if a["id"] == "EVIDENCE-FRESHNESS"
            else 1
            if a["id"].startswith("LOCAL-GATE-")
            else 2,
            -sum(a["id"] in b["dependencies"] for b in actions),
            a["id"],
        ),
    )[:3]
    dump(
        out / "results.json",
        {
            "checks": checks,
            "usage": dict(usage),
            "verdict_counts": dict(collections.Counter(c["verdict"] for c in checks)),
            "stale_files": sorted(stale),
        },
    )
    dump(
        out / "actions.json",
        {
            "schema_version": 2,
            "actions": actions,
            "next_three_ready": [a["id"] for a in ready],
            "delta": delta,
        },
    )
    lines = [
        "# Evidence-to-action progress report",
        "",
        "Advisory review only: source support does not establish live or graphical acceptance.",
        "",
        "## Freshness",
        ", ".join(sorted(stale)) if stale else "No tracked snapshot changes detected.",
        "",
        "## Local validation",
        f"{len(passed)} distinct fresh passing commands; {len(records)} execution records. See [validation.json](validation.json).",
        "",
        "## Next three ready tasks",
    ]
    lines += [f"- **{a['id']}**: {a['next_step']}" for a in ready] or [
        "No ready actions; resolve freshness, preparation or authorization blockers."
    ]
    lines += ["", "## Blocked tasks"] + [
        f"- **{a['id']}**: {a['blocker']}" for a in actions if a["blocker"]
    ]
    lines += [
        "",
        "## Baseline delta",
        f"New: {len(delta['new'])}; unchanged: {len(delta['unchanged'])}; status changes: {len(delta['status_changes'])}; not reassessed: {len(delta['not_reassessed'])} (not closed).",
        "",
        "See [actions.json](actions.json) for evidence, exact next steps and acceptance criteria; [results.json](results.json) for per-check judgments. Failures and unassessed batches never receive inferred verdicts. No acceptance action is auto-closed.",
    ]
    (out / "report.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    return actions


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--baseline", type=Path)
    args = parser.parse_args()
    summarize(args.directory.resolve(), ROOT, args.baseline)


if __name__ == "__main__":
    main()
