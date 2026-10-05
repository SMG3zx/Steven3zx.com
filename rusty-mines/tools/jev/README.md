# Jev Rust question harness

Standalone developer tooling, not part of the Minecraft gateway or deterministic SpacetimeDB reducers. Reads the current TypeSafe v1 HTTP contract: <https://docs.typesafe.ai/api.md>. Design guidance: <https://raw.githubusercontent.com/typesafe-ai/skills/refs/heads/main/skills/typesafe-ai/SKILL.md> and the citation-check cookbook <https://docs.typesafe.ai/cookbooks/citation_check.md>.

Jev supplies typed judgments, **not conversational text or reasoning explanations**. Give it explicit evidence and narrow questions; request a Noul (yes probability), Choice (one candidate and distribution/confidence), or Score (position on 2–10 described levels). Independent questions run together over the same state. Question IDs are routing keys, not instructions seen by the model.

## Use

From the repository root:

```sh
cargo test --manifest-path tools/jev/Cargo.toml
cargo run --manifest-path tools/jev/Cargo.toml -- tools/jev/examples/review.json --dry-run
cargo run --manifest-path tools/jev/Cargo.toml -- tools/jev/examples/review.json
```

For a live call, securely set `TYPESAFE_API_KEY` in the process environment using your local secret manager. Rotate any key pasted into a chat. Do not put keys in request JSON, command arguments, committed files, or logs. The harness does not load `.env`, saved gateway tokens, repository files, or credentials automatically. `-` in place of a filename reads request JSON from stdin. Dry-run needs no key and makes no network request, but prints the request: do not dry-run confidential state into shared logs.

Use the example as a request template; replace state and instructions with your own question/evidence. All three primitives support string/object/array instructions. Choice descriptions can be null. Noul criteria are optional; if supplied, define `true` and `false`. Model defaults to `jev-latest`; pin a documented model version when reproducibility matters. The response records the actual model, typed answers, probabilities and token usage.

## Limits and privacy

- Live calls send **all explicit state and questions** to TypeSafe and may incur billing. Review/redact code, player data, logs, and secrets before submitting. No automatic source collection or uploads.
- Harness limits: 1 MiB input/response, 64 questions/request, 10-second connection and 60-second request timeout. These are local safeguards, not claims about service limits.
- Fixed HTTPS endpoint; redirects disabled. No auto-retry or automatic action execution. On 429/529, wait and retry deliberately; repeated calls may cost more. Non-success bodies are not logged because they may echo private state.
- Typed responses and their IDs, option sets, probability ranges/sums and score range are validated. Probabilities do not prove truth, protocol correctness, authorization, or complete test coverage.
- Stdout contains answers (possibly source-derived option names/legend) and usage; treat it as potentially sensitive. No persistent request/response cache is created.

## Recommended rusty-mines experiments

1. **Evidence review (start here):** supply a plan claim and selected code/test records. Batch Choice support/contradiction/insufficiency judgments with separate missing-live/missing-client Nouls. A human reviews; never auto-check roadmap boxes.
2. **Packet/code routing:** retrieve a small candidate list with deterministic search, then ask Choice which packet/module best matches a task. Include `none` and `insufficient_evidence`. Exact IDs remain catalog lookups, not model predictions.
3. **Failure triage:** supply a redacted failing trace and candidate categories (codec, lifecycle, backend authorization, subscription timing, unsupported feature, unknown). Use judgments to prioritize investigation, not to claim root cause without reproduction.
4. **Refactor boundary review:** compare a specific function against responsibilities (wire encoding, session lifecycle, backend authority, world policy). Multi-label Nouls can flag mixed responsibilities; code changes still require tests.
5. **Test-gap prioritization:** score concrete cases against independent impact and missing-evidence rubrics. Keep estimated effort explicit rather than allowing the model to invent facts; combine weights in Rust outside inference.
6. **Spec/code claim verification:** provide pinned official field evidence and the exact implementation. Use one judgment per claim; retain manual/codec checks for offsets, lengths, arithmetic and registry IDs.

Keep known rules and calculations in code. Do not use Jev for movement authority, inventory conservation, permission decisions, deterministic reducer logic, or packet flood handling. Untrusted text can manipulate model judgments; never let a judgment bypass an authorization gate or execute commands.

## Snapshot-bound progress and action reports

From the repository root (Python 3.9+):

```sh
# Selection/size audit and action report only; no tests, credentials or API calls.
python tools/jev/progress_review.py --offline

# Run safe local gates, audit evidence and summarize; still no API calls.
python tools/jev/progress_review.py

# Explicitly authorize billed uploads for a selected batch.
python tools/jev/progress_review.py --live --batch world_worker

# Compare against the previous run's action/verdict records.
python tools/jev/progress_review.py --offline --baseline docs/progress-review-20261004T230800Z

# Python regression tests do not access credentials, network or databases.
python -m unittest discover -s tools/jev -p test_progress_review.py -v
```

Each run gets a unique `docs/progress-review-*` directory containing `manifest.json`, per-batch `coverage.json`, bounded requests, execution logs, `validation.json`, `results.json`, `actions.json` and `report.md`. **Offline output is preparation, not a new Jev result or passing execution.** The semantic scope remains focused; the freshness snapshot includes Rust source, tests, assets, Cargo/build inputs and review scripts, excluding credentials and build outputs.

The runner checks hashes before/after validation and before/after uploads. Added, deleted or modified tracked inputs invalidate the snapshot. Exit codes: 0 completed preparation/review, 1 API failure, 2 stale snapshot, 3 preparation/coverage error, 4 local validation failure. A successful offline exit does not imply acceptance. Missing required functions fail preparation before any upload; extraction is indentation-based, not a Rust parser, so inspect coverage ranges and omitted dependencies.

To resume, use `--resume` with an existing **schema-version-2** run directory. Successful responses are not resent. Previously failed requests are retried only with both `--live` and `--retry-failed`; stale runs cannot resume. `--batch` can select one or more batches already prepared in that run. Live mode loads only `TYPESAFE_API_KEY` from the process environment or `tools/jev/.env`; it never accesses gateway credentials or provisions databases. Build the Rust harness before the first live call with `cargo build --manifest-path tools/jev/Cargo.toml --bin jev-harness`.

### Finding triage

Source, local test-design and execution questions are separate. Missing/invalid responses become `not_assessed`. Source support never proves runtime acceptance; a contradiction becomes an investigation, not a code defect. Actions have stable IDs, categories, priorities, files, evidence references, exact next steps, dependencies, blockers and acceptance criteria. Reports list the next three ready tasks. Baseline disappearance means **not reassessed**, never completed.

An independent reviewer may add `triage.json` inside the run directory, keyed by question ID:

```json
{
  "mode_wire_sync": {
    "category": "reviewer_disagreement",
    "evidence_refs": ["independent-mode-inspection.md"],
    "next_step": "Validate all four Game Event values and ability flags against the official client",
    "acceptance_criteria": ["Record wire assertions for Survival, Creative, Adventure and Spectator"]
  }
}
```

Referenced evidence files must actually exist inside the repository. Allowed independent classifications are `confirmed_defect`, `reviewer_disagreement` and `out_of_scope`. Staleness overrides triage. Arbitrary manual status fields do not close acceptance actions. Rebuild reports with `python tools/jev/summarize_progress.py` followed by the run directory and optional `--baseline` directory; this preserves the collection manifest and raw responses. Only the local freshness action may become verified automatically, after every required local command passes against an unchanged snapshot. Live and graphical actions require independent execution and remain open in this tooling.

Neither default validation nor live Jev review publishes/resets a database, runs ignored host tests, controls graphical clients, or automatically changes plan/roadmap acceptance.

## Validation status

Unit tests and dry-run are local contract checks. A successful paid API request is a separate live validation gate; do not infer it from compilation or fixtures.
