"""Offline regression tests; no credentials, network or database access."""

import hashlib
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import granular_review as granular
import progress_review as progress
import summarize_progress as summary


class ReviewTests(unittest.TestCase):
    def test_next_tasks_prioritize_freshness_and_failed_local_gate(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            out = self.fixture(root)
            (out / "failed.log").write_text("failure", encoding="utf-8")
            summary.dump(
                out / "validation.json",
                [
                    {
                        "command": ["cargo", "test"],
                        "exit_code": 1,
                        "fresh": True,
                        "log": "failed.log",
                    }
                ],
            )
            summary.summarize(out, root)
            self.assertEqual(
                summary.load(out / "actions.json", {})["next_three_ready"][:2],
                ["EVIDENCE-FRESHNESS", "LOCAL-GATE-1"],
            )

    def test_outcome_codes(self):
        self.assertEqual(progress.outcome_code({}, []), 0)
        self.assertEqual(progress.outcome_code({}, [{"exit_code": 101}]), 4)
        self.assertEqual(
            progress.outcome_code({"api_failures": {"batch": "failed"}}, []), 1
        )
        self.assertEqual(
            progress.outcome_code(
                {"changed_during_review": ["source"]}, [{"exit_code": 0}]
            ),
            2,
        )

    def test_selection_records_ranges_and_duplicate_names(self):
        text = "fn a() {\n    work();\n}\nfn a() {\n}\n"
        ranges, excerpt = granular.selection(text, ["a"])
        self.assertEqual([r["start_line"] for r in ranges], [1, 4])
        self.assertIn("2:     work();", excerpt)

    def test_missing_selection_fails_before_upload(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "source.rs").write_text("fn present() {\n}\n", encoding="utf-8")
            groups = {
                "sample": {
                    "sources": ["source.rs"],
                    "functions": ["missing"],
                    "checks": {},
                }
            }
            with (
                patch.object(granular, "ROOT", root),
                patch.object(granular, "OUT", root / "out"),
                patch.object(granular, "GROUPS", groups),
            ):
                with self.assertRaisesRegex(ValueError, "required functions missing"):
                    granular.prepare()
            self.assertEqual(
                json.loads((root / "out/sample.coverage.json").read_text())["missing"],
                ["missing"],
            )

    def test_snapshot_detects_modification_and_added_source(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "src").mkdir()
            file = root / "src/a.rs"
            file.write_text("old", encoding="utf-8")
            manifest = {"source_sha256": progress.hashes(root, ["src/a.rs"])}
            file.write_text("new", encoding="utf-8")
            (root / "src/b.rs").write_text("added", encoding="utf-8")
            with patch.object(progress, "ROOT", root):
                self.assertFalse(progress.checkpoint(root, manifest, "test"))
            self.assertIn("src/a.rs", manifest["changed_during_review"])
            self.assertIn("src/b.rs", manifest["changed_during_review"])

    def fixture(self, root, verdict=None):
        source = root / "src.rs"
        source.write_text("fn sample() {}", encoding="utf-8")
        h = hashlib.sha256(source.read_bytes()).hexdigest()
        out = root / "run"
        out.mkdir()
        summary.dump(
            out / "manifest.json", {"source_sha256": {"src.rs": h}, "api_failures": {}}
        )
        summary.dump(out / "validation.json", [])
        summary.dump(out / "sample.coverage.json", {"missing": []})
        summary.dump(
            out / "sample.request.json",
            {
                "state": {"manifest": [{"path": "src.rs", "sha256": h}]},
                "questions": {
                    "case": {
                        "criteria": {
                            "supports": "yes",
                            "contradicts": "no",
                            "insufficient_evidence": "unknown",
                        },
                        "instructions": {
                            "claim": "Source implements the selected behavior."
                        },
                    }
                },
            },
        )
        if verdict:
            summary.dump(
                out / "sample.response.json",
                {
                    "answers": {
                        "case": {
                            "type": "choice",
                            "choice": verdict,
                            "confidence": 0.9,
                            "probabilities": {
                                k: 1.0 if k == verdict else 0.0
                                for k in (
                                    "supports",
                                    "contradicts",
                                    "insufficient_evidence",
                                )
                            },
                        }
                    }
                },
            )
        return out

    def test_missing_response_is_not_assessed_and_not_closed(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            out = self.fixture(root)
            actions = summary.summarize(out, root)
            check = json.loads((out / "results.json").read_text())["checks"][0]
            self.assertEqual(check["verdict"], "not_assessed")
            self.assertFalse(any(a["status"] == "verified" for a in actions))

    def test_contradiction_requires_independent_triage(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            out = self.fixture(root, "contradicts")
            actions = summary.summarize(out, root)
            self.assertEqual(actions[-1]["category"], "reviewer_disagreement")
            summary.dump(
                out / "triage.json",
                {
                    "case": {
                        "category": "confirmed_defect",
                        "evidence_refs": ["reproduction.log"],
                        "next_step": "Fix reproduced case",
                    }
                },
            )
            with self.assertRaisesRegex(ValueError, "existing local evidence"):
                summary.summarize(out, root)
            (out / "reproduction.log").write_text(
                "Independent failing reproduction", encoding="utf-8"
            )
            actions = summary.summarize(out, root)
            self.assertEqual(actions[-1]["category"], "confirmed_defect")

    def test_stale_support_cannot_verify_action(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            out = self.fixture(root, "supports")
            (root / "src.rs").write_text("changed", encoding="utf-8")
            actions = summary.summarize(out, root)
            self.assertEqual(actions[-1]["category"], "stale_evidence")
            self.assertEqual(actions[-1]["status"], "planned")
            self.assertFalse(any(a["status"] == "verified" for a in actions))

    def test_invalid_response_is_not_assessed(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            out = self.fixture(root, "invented")
            summary.summarize(out, root)
            self.assertEqual(
                json.loads((out / "results.json").read_text())["checks"][0]["verdict"],
                "not_assessed",
            )

    def test_probability_validation(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            out = self.fixture(root, "supports")
            request = summary.load(out / "sample.request.json", {})
            response = summary.load(out / "sample.response.json", {})
            self.assertTrue(summary.valid_response(request, response))
            response["answers"]["case"]["confidence"] = float("nan")
            self.assertFalse(summary.valid_response(request, response))

    def test_local_verification_needs_all_commands_and_logs(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            out = self.fixture(root)
            commands = progress.validation_commands()
            records = []
            for i, command in enumerate(commands):
                name = f"validation-{i}.log"
                (out / name).write_text("exit zero", encoding="utf-8")
                records.append(dict(command=command, exit_code=0, fresh=True, log=name))
            summary.dump(out / "validation.json", records)
            self.assertEqual(summary.summarize(out, root)[0]["status"], "verified")
            (out / records[0]["log"]).unlink()
            self.assertNotEqual(summary.summarize(out, root)[0]["status"], "verified")

    def test_baseline_disappearance_is_not_completion(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            out = self.fixture(root)
            previous = root / "previous"
            previous.mkdir()
            summary.dump(
                previous / "actions.json",
                {"actions": [{"id": "OLD", "status": "ready"}]},
            )
            summary.summarize(out, root, previous)
            delta = json.loads((out / "actions.json").read_text())["delta"]
            self.assertEqual(delta["not_reassessed"], ["OLD"])

    def test_rejects_cycles_and_missing_criteria(self):
        action = dict(
            id="A",
            category="missing_test",
            status="ready",
            next_step="test",
            acceptance_criteria=["pass"],
            evidence_refs=["log"],
            files=[],
            dependencies=["A"],
            priority="P0",
        )
        with self.assertRaisesRegex(ValueError, "Cyclic"):
            summary.validate_actions([action])
        action["dependencies"] = []
        action["acceptance_criteria"] = []
        with self.assertRaisesRegex(ValueError, "missing acceptance"):
            summary.validate_actions([action])


if __name__ == "__main__":
    unittest.main()
