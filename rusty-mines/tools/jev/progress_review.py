"""Snapshot-bound evidence collection. Offline by default; --live authorizes billed uploads."""

import argparse
import copy
import datetime
import hashlib
import json
import subprocess
import sys
from pathlib import Path

import granular_review as review

ROOT = review.ROOT


def dump(path, value):
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False), encoding="utf-8")


def hashes(root, paths):
    return {
        p: hashlib.sha256((root / p).read_bytes()).hexdigest()
        if (root / p).is_file()
        else None
        for p in paths
    }


def tracked_paths(root):
    # Include build inputs and tests, not credentials, outputs or historical reports.
    paths = set()
    for directory in (
        "src",
        "spacetimedb/src",
        "inventory-core/src",
        "tools/jev/src",
        "tests",
        "assets",
    ):
        base = root / directory
        if base.exists():
            paths.update(
                p.relative_to(root).as_posix()
                for p in base.rglob("*")
                if p.is_file() and p.name != ".env"
            )
    for directory in ("", "spacetimedb", "inventory-core", "tools/jev"):
        for name in ("Cargo.toml", "Cargo.lock", "build.rs"):
            p = root / directory / name
            if p.is_file():
                paths.add(p.relative_to(root).as_posix())
    paths.update(
        "tools/jev/" + n
        for n in (
            "granular_review.py",
            "progress_review.py",
            "summarize_progress.py",
            "test_progress_review.py",
        )
    )
    paths.add("docs/play-implementation-plan.md")
    return sorted(paths)


def configure():
    groups = copy.deepcopy(review.GROUPS)
    groups["acceptance_records"]["sources"].remove("docs/play-packet-roadmap.md")
    player = groups["player_state"]
    player["sources"].append("src/gateway.rs")
    player["whole_sources"] = ["spacetimedb/src/world.rs"]
    player["functions"] += [
        "respawn",
        "reset_loading",
        "persist_player_state",
        "player_state",
        "player_state_update",
        "player_respawn_request",
        "requires_persistent_player_state",
        "void_death_freezes_until_same_dimension_respawn_and_reloads_chunks",
        "claim_player_state",
        "release_player_state",
        "update_player_vitals",
        "request_player_respawn",
        "gateway_player_state",
    ]
    groups["world_worker"]["functions"] += [
        "block_actions_reconcile_rejections_confirm_commits_and_isolate_late_results",
        "chunk_load_reconciles_snapshot_changes_during_and_after_batch_emission",
    ]
    groups["local_test_design"] = {
        "sources": ["src/connection.rs"],
        "functions": [
            "block_actions_reconcile_rejections_confirm_commits_and_isolate_late_results",
            "chunk_load_reconciles_snapshot_changes_during_and_after_batch_emission",
            "void_death_freezes_until_same_dimension_respawn_and_reloads_chunks",
        ],
        "checks": {
            "local_edit_test_design": "Supplied test bodies exercise rejected-edit correction, confirmed edits and late-result isolation; this is coverage design, not live execution.",
            "local_chunk_test_design": "Supplied test bodies exercise override changes during and after chunk batch emission; this is coverage design, not graphical execution.",
            "local_respawn_test_design": "Supplied test bodies exercise freezing after void death and same-dimension respawn with renewed chunk loading; this is coverage design, not graphical execution.",
        },
    }
    return groups


def validation_commands():
    commands = [
        ["cargo", "test", "--all-targets"],
        ["cargo", "clippy", "--all-targets", "--", "-D", "warnings"],
        ["cargo", "fmt", "--all", "--", "--check"],
    ]
    for manifest in (
        "inventory-core/Cargo.toml",
        "spacetimedb/Cargo.toml",
        "tools/jev/Cargo.toml",
    ):
        commands += [
            ["cargo", "test", "--manifest-path", manifest, "--all-targets"],
            [
                "cargo",
                "clippy",
                "--manifest-path",
                manifest,
                "--all-targets",
                "--",
                "-D",
                "warnings",
            ],
            ["cargo", "fmt", "--manifest-path", manifest, "--all", "--", "--check"],
        ]
    commands.append(
        [
            "cargo",
            "check",
            "--manifest-path",
            "spacetimedb/Cargo.toml",
            "--target",
            "wasm32-unknown-unknown",
        ]
    )
    commands.append(
        [
            sys.executable,
            "-m",
            "unittest",
            "discover",
            "-s",
            "tools/jev",
            "-p",
            "test_progress_review.py",
            "-v",
        ]
    )
    return commands


def checkpoint(out, manifest, stage):
    current = hashes(ROOT, manifest["source_sha256"])
    added = sorted(set(tracked_paths(ROOT)) - set(manifest["source_sha256"]))
    changed = (
        sorted(p for p, h in current.items() if h != manifest["source_sha256"][p])
        + added
    )
    manifest.setdefault("checkpoints", []).append(
        {
            "stage": stage,
            "utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "changed": changed,
        }
    )
    manifest["changed_during_review"] = sorted(
        set(manifest.get("changed_during_review", [])) | set(changed)
    )
    dump(out / "manifest.json", manifest)
    return not manifest["changed_during_review"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--live", action="store_true")
    parser.add_argument(
        "--offline",
        action="store_true",
        help="Prepare and audit only; no tests or network",
    )
    parser.add_argument("--batch", action="append", choices=list(configure()))
    parser.add_argument("--resume", type=Path)
    parser.add_argument("--retry-failed", action="store_true")
    parser.add_argument(
        "--baseline",
        type=Path,
        help="Previous review directory for action/verdict deltas",
    )
    args = parser.parse_args()
    if args.offline and args.live:
        parser.error("--offline and --live are mutually exclusive")
    if args.retry_failed and not (args.resume and args.live):
        parser.error("--retry-failed requires --resume and --live")
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    out = (
        args.resume.resolve()
        if args.resume
        else ROOT / "docs" / ("progress-review-" + stamp)
    )
    if args.resume and (
        not out.is_relative_to(ROOT / "docs") or not (out / "manifest.json").is_file()
    ):
        parser.error("Resume requires an existing review directory under docs")
    groups = configure()
    if args.batch:
        groups = {k: v for k, v in groups.items() if k in args.batch}
    review.GROUPS, review.OUT = groups, out
    if args.resume:
        manifest = json.loads((out / "manifest.json").read_text(encoding="utf-8"))
        if manifest.get("schema_version") != 2:
            parser.error("Resume requires a snapshot-bound schema version 2 review")
        if not checkpoint(out, manifest, "resume"):
            print("Stale snapshot; create a new review instead of resuming.")
            from summarize_progress import summarize

            summarize(out, ROOT, args.baseline)
            return 2
        groups = {
            k: v for k, v in groups.items() if (out / f"{k}.request.json").is_file()
        }
        if not groups:
            parser.error("No selected batches were prepared in this run")
        review.GROUPS = groups
    else:
        out.mkdir()
        manifest = {
            "schema_version": 2,
            "created_utc": stamp,
            "source_sha256": hashes(ROOT, tracked_paths(ROOT)),
            "scope": "Focused M6/M7 source and acceptance; local build-input snapshot",
            "api_failures": {},
            "baseline": str(args.baseline.resolve()) if args.baseline else None,
        }
        dump(out / "manifest.json", manifest)
        records = []
        if not args.offline:
            for i, command in enumerate(validation_commands()):
                if not checkpoint(out, manifest, "before_validation"):
                    break
                try:
                    result = subprocess.run(
                        command,
                        cwd=ROOT,
                        capture_output=True,
                        encoding="utf-8",
                        errors="replace",
                        timeout=180,
                    )
                    log, code = result.stdout + result.stderr, result.returncode
                except (subprocess.TimeoutExpired, OSError):
                    log, code = (
                        "Command could not complete; not a passing result.",
                        None,
                    )
                name = f"validation-{i + 1}.log"
                (out / name).write_text(log, encoding="utf-8")
                fresh = checkpoint(out, manifest, "after_validation")
                records.append(
                    {
                        "command": command,
                        "exit_code": code,
                        "log": name,
                        "snapshot_ref": "manifest.json#/source_sha256",
                        "fresh": fresh,
                    }
                )
                print("Validation", i + 1, "exit", code, flush=True)
        dump(out / "validation.json", records)
        if not checkpoint(out, manifest, "before_selection"):
            from summarize_progress import summarize

            summarize(out, ROOT, args.baseline)
            return 2
        try:
            review.prepare()
        except ValueError as error:
            manifest["preparation_error"] = str(error)
            dump(out / "manifest.json", manifest)
            print(error)
            from summarize_progress import summarize

            summarize(out, ROOT, args.baseline)
            return 3
        for group in groups:
            path = out / f"{group}.request.json"
            req = json.loads(path.read_text(encoding="utf-8"))
            req["state"]["local_validation"] = records
            req["state"]["policy"] = (
                "Source judgments are advisory, not execution evidence. Local validation does not prove live or graphical acceptance. Omitted evidence requires insufficient_evidence."
            )
            dump(path, req)
    if not checkpoint(out, manifest, "before_upload"):
        from summarize_progress import summarize

        summarize(out, ROOT, args.baseline)
        return 2
    if args.live:
        for group in groups:
            response = out / f"{group}.response.json"
            if response.exists() or (
                group in manifest["api_failures"] and not args.retry_failed
            ):
                continue
            if not checkpoint(out, manifest, "before_upload_" + group):
                break
            review.GROUPS = {group: groups[group]}
            try:
                review.live()
                manifest["api_failures"].pop(group, None)
            except (RuntimeError, subprocess.TimeoutExpired, OSError):
                # Never persist provider stderr, which could echo submitted secrets.
                manifest["api_failures"][group] = (
                    "Harness call failed or typed response was rejected; no verdict accepted."
                )
            checkpoint(out, manifest, "after_upload_" + group)
    checkpoint(out, manifest, "complete")
    from summarize_progress import summarize

    summarize(out, ROOT, args.baseline)
    print("Artifacts:", out)
    records = json.loads((out / "validation.json").read_text(encoding="utf-8"))
    return outcome_code(manifest, records)


def outcome_code(manifest, records):
    if manifest.get("changed_during_review"):
        return 2
    if manifest.get("api_failures"):
        return 1
    if any(r.get("exit_code") != 0 for r in records):
        return 4
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
