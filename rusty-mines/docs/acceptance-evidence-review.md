# Plan and roadmap acceptance evidence review

Date: 2026-10-04. Model judgments are advisory; source inspection, test execution and graphical acceptance remain separate.

## Executed review

The Rust builder selected eight files: the plan, roadmap, connection, gateway, Play codec, backend world/foundation and opt-in live tests. Full-file FNV-1a fingerprints record freshness, not cryptographic integrity. The original approximately 499,000-character full-source request returned HTTP 400; its error body was not inspected, so the exact cause is unconfirmed. The documented Jev state-plus-longest-question limit is 32k tokens. The builder was changed to bounded line-numbered excerpts (up to 10,000 bytes per file), with explicit omitted-evidence notices.

The subsequent request **succeeded**, using `jev-1.13.0`: **17,605 input tokens, 403 output tokens**. Credentials were loaded only into the child process from `tools/jev/.env`; no key was printed, copied to source or stored in request/results. The model received selected project source and documentation. No gateway/backend/graphical tests were executed as part of this inference.

Raw response: [acceptance-evidence-jev-response.json](acceptance-evidence-jev-response.json). Reviewed source manifest and deterministic audit: [acceptance-evidence-source-audit.json](acceptance-evidence-source-audit.json). Those fingerprints refer to the input snapshot before these report updates; they are not claims about later source revisions.

## Jev results

| Claim evaluated | Verdict | P(supports) | P(contradicts) | P(insufficient) | Confidence |
| --- | --- | ---: | ---: | ---: | ---: |
| Plan baseline matches source | Insufficient evidence | 0.39 | 0.15 | 0.46 | 0.18 |
| Roadmap baseline matches source | Supports | 0.44 | 0.15 | 0.41 | 0.15 |
| Supported edit ACKs require reducer/subscription confirmation | Supports | 0.53 | 0.34 | 0.13 | 0.29 |
| Survival edits and inventory commit atomically | Insufficient evidence | 0.13 | 0.06 | 0.81 | 0.71 |
| Evidence fully establishes reload/reconnect/backend-restart persistence | Insufficient evidence | 0.00 | 0.44 | 0.56 | 0.33 |
| Evidence establishes accepted M7 behavior including graphical validation | Contradicts | 0.00 | 0.94 | 0.06 | 0.91 |
| Evidence establishes current live mutation/race acceptance | Contradicts | 0.00 | 0.99 | 0.01 | 0.99 |
| Evidence establishes full codec semantics/malformed coverage | Contradicts | 0.00 | 0.93 | 0.07 | 0.89 |

These are evaluations of explicit propositions, **not findings that the documents falsely claimed completed acceptance**. The documents already state important acceptance gaps. In particular, contradicting the hypothetical claim of graphical acceptance agrees with leaving that gate open. Low-confidence support is not a basis for promoting a packet or milestone.

## Independent verification and decisions

- `src/connection.rs`, `poll_block_action`: the successful edit path waits for reducer `Ok(())`, then a subscribed action with matching sequence, coordinates, expected state and result. It refreshes inventory and corrects the block before ACK. Rejected/read-only actions also send ACKs after correction; those are prediction reconciliation, not successful mutation claims. Retain this distinction in further review questions.
- `spacetimedb/src/world.rs`, `apply_block_action`: Survival item consumption/drop collection, inventory revision update, block override mutation and action result occur within one reducer. Source supports same-transaction intent under SpacetimeDB reducer semantics. Host rollback/race acceptance still needs actual live tests. The excerpt-based Jev result missed enough context to establish this; it is **not evidence that atomicity is absent**.
- No new restart persistence test, live reducer race test, graphical session or comprehensive codec review ran here. Keep those acceptance gates open.
- Baseline consistency judgments are too uncertain to warrant speculative source-status rewrites. Selected excerpts do not establish full repository correctness, especially where the model saw no selected functions from a file.
- No packet status or milestone checkbox was promoted based on model output.

## Deterministic roadmap audit

213 rows; 213 unique `(direction, ID)` keys; no duplicates. I=25 (11.7%), P=38 (17.8%), unimplemented=150 (70.4%). I/P coverage=63 (29.6%). Corrected summary labels previously claiming 30 CB/31 SB/61 combined handled IDs and 215 rows to 31 CB/32 SB/63 combined I/P IDs and 213 rows. These percentages describe documentation labels, not full vanilla gameplay acceptance.

## Tool validation and follow-up

The initial reviewer/harness suite passed six tests and Clippy with warnings denied. The bounded-excerpt revision is tested separately in the current execution; none of these tool checks validates game behavior.

Highest-value next evidence:

1. Run current disposable-backend edit/inventory mutation tests, including rollback, revocation/expiry and concurrent edits.
2. Record chunk unload/reload, reconnect and backend restart persistence, including snapshot/update ordering.
3. Perform two-client graphical acceptance for loading, inventory, replication, chat, editing, modes and limited death recovery.
4. Complete independent component-field/hash semantics and malformed-input review.

From the repository root, build a fresh request and review/redact it before submission:

```sh
cargo run --manifest-path tools/jev/Cargo.toml --bin evidence-review -- . tools/jev/target/evidence-review
cargo run --manifest-path tools/jev/Cargo.toml -- tools/jev/target/evidence-review/request.json --dry-run
```

With `TYPESAFE_API_KEY` securely loaded into the process environment:

```sh
cargo run --manifest-path tools/jev/Cargo.toml -- tools/jev/target/evidence-review/request.json > tools/jev/target/evidence-review/response.json
```

Live calls share the explicit evidence with TypeSafe and may incur charges. The CLI itself does not automatically load `.env`; never paste keys into commands or committed reports. Generated request/response files remain under ignored `target/`. Basic secret screening is not a complete privacy guarantee.
