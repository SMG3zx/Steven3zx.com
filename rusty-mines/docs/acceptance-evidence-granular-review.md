# Granular plan/roadmap evidence review

Date: 2026-10-04. Four authorized requests to pinned `jev-1.13.0`, eight checks each: **32 checks**, **54,635 input tokens**, **1,537 output tokens**. Results: 19 supports, 7 contradicts, 6 insufficient evidence. These counts are not a completion percentage; source checks and actual execution claims are different categories.

Raw probabilities: [acceptance-evidence-granular-results.json](acceptance-evidence-granular-results.json). Per-request source SHA-256 fingerprints: [acceptance-evidence-granular-manifest.json](acceptance-evidence-granular-manifest.json). Different requests/inspection times must not be assumed to share one repository revision. Source is actively changing; recheck fingerprints before reusing a finding.

## Backend edit design

| Check | Jev verdict | Confidence |
| --- | --- | ---: |
| Gateway identity plus exact connection ownership | Supports | 0.82 |
| Unexpired lease and active Play phase | Supports | 1.00 |
| Server-known reach and owned chunk interest | Supports | 0.99 |
| Survival/Creative policy; unsupported Adventure/Spectator edits denied | Supports | 0.90 |
| Survival inventory and block updates within one reducer | Supports | 0.86 |
| Exact retries idempotent; conflicting/out-of-order requests denied | Supports | 0.81 |
| Per-chunk and world override limits | Supports | 0.99 |
| Administrator authority for elevated mode grants | Supports | 0.99 |

These support source design, not host rollback, security proof, deployment compatibility or live transaction acceptance. The narrower atomicity request resolves the broad review's missing-context result without changing implementation.

## Worker edit and chunk design

| Check | Jev verdict | Confidence |
| --- | --- | ---: |
| At most one pending block action | Supports | 0.95 |
| Successful edit waits for reducer callback | Supports | 0.84 |
| Successful edit requires matching subscribed result | Supports | 0.95 |
| Denied/unsupported action correction before prediction ACK | Supports | 0.81 |
| Confirmation deadline and fail-closed behavior | Supports | 0.99 |
| Override byte/record/coordinate validation | Supports | 0.98 |
| Revision-based loaded snapshot ordering | Supports | 0.79 |
| Base chunk reload reconciled with current overrides | Insufficient evidence | 0.27 |

Priority remaining test: chunk snapshot arrival before/after base-chunk emission, edits during construction, replacement interest, unload/reload and reconnect. A revision field alone does not prove all ordering races are handled. Successful mutation ACKs and rejection prediction-reconciliation ACKs are distinct paths.

## Player-state source

| Check | Jev verdict | Confidence | Follow-up interpretation |
| --- | --- | ---: | --- |
| Client cannot grant itself flight | Supports | 0.95 | Source-design support only |
| All-mode game-mode/ability wire synchronization | Contradicts | 0.39 | **Do not accept as a defect**: inspected `send_mode_state` emits Game Event value from `self.game_mode`; polling maps all four modes |
| Server-owned normal-mode gravity/ground | Supports | 1.00 | Limited platform simulation, not general physics |
| Bounded simulation catch-up | Supports | 0.98 | Eight-tick work cap observed in source |
| Survival-only sprint food loss | Supports | 0.97 | Gateway-local state; acceptance still pending |
| Persistent vitals across reconnect | Insufficient evidence | 0.93 | Needs explicit persistence policy and evidence |
| Death/Respawn packet lifecycle plus renewed readiness | Insufficient evidence | 0.42 | Dedicated `respawn()` was not in the selected function list; insufficient coverage |
| Collision follows edited terrain | Insufficient evidence | 0.86 | Current simulation inspected uses fixed platform Y=65; edited-terrain collision is outside selected scope |

Independent follow-up found source `respawn()` emitting the Respawn packet, restoring health/pose, clearing chunk state and sending a teleport; void simulation emits zero health and Combat Death. This contradicts the plan's older statement that only corrective-teleport recovery exists. It does **not** establish complete loading reset, food/inventory reconciliation, backend authority/persistence, official fixture or graphical acceptance. Document the partial source increment rather than marking death/respawn accepted. The review itself did not run the newly present death/respawn test.

## Acceptance execution records

| Claimed completed evidence | Jev verdict | Confidence |
| --- | --- | ---: |
| Current M6/M7 live mutations, not just schema registration | Contradicts | 0.98 |
| Host rollback with no partial block/inventory writes | Insufficient evidence | 0.46 |
| Backend restart and client reload preserve edits | Contradicts | 0.26 |
| Two official graphical clients see committed edits/entities | Contradicts | 0.87 |
| Concurrent edits, revocation and lease-expiry races | Contradicts | 0.36 |
| Current slow-client/flood backpressure acceptance | Insufficient evidence | 0.55 |
| Independent component/hash semantic review | Contradicts | 0.75 |
| Graphical all-mode death/respawn/physics acceptance | Contradicts | 0.92 |

These evaluate hypothetical completion claims; they do not accuse documents of claiming success. Existing historical inventory live evidence remains historical evidence and is not erased. No gameplay/backend/graphical acceptance test ran in this review. Low-confidence contradictions about restart/races must be treated as unresolved evidence gaps, not proof of failure.

## Next work, at actionable granularity

1. Verify success vs rejection ACK ordering, matching subscription state, timeout and late-session results in targeted worker tests.
2. Run disposable-backend atomic edit/inventory rollback, conflict/retry, ownership, reach, interest, revocation/expiry and cap tests.
3. Test base-chunk/overlay ordering and edit visibility on unload/reload, reconnect and actual backend restart.
4. Review new death/respawn source against loading reset, teleport readiness, food/cursor/pending-action cleanup and packet fixtures; then execute its tests.
5. Decide/document vitals persistence; don't infer it from gateway-local health/food fields.
6. Perform the two-client graphical acceptance matrix and independent component/hash review.

## Reproduce

Prepare locally without an API request:

```sh
python tools/jev/granular_review.py
```

Explicitly submit four billed reviews through the Rust harness (the runner loads only `TYPESAFE_API_KEY` from the environment or `tools/jev/.env`):

```sh
python tools/jev/granular_review.py --live
```

Requests contain explicit source/doc evidence; inspect them under `tools/jev/target/granular-review/` before submission. Model responses are advisory, never automatic permissions or acceptance-box updates. The function-excerpt selector is a lightweight indentation-based tool, not a Rust parser; review selection boundaries and missing helper dependencies before interpreting insufficient evidence as a defect.
