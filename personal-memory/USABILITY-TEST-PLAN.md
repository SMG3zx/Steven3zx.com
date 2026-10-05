# Personal Memory usability test plan

This plan is the next evidence step for the end-to-end UX goal. It is designed for a moderated usability study of the local web interface and does not require real private memories, production credentials, or external data transmission.

## Research questions

1. Can a user tell when chat is working, which memory scope is active, and whether the turn was saved?
2. Can a user explain why a response used a memory without opening the technical trace?
3. Can a user find the source of a memory, correct it, and understand supersession?
4. Can a user retract a memory after seeing its source/replacement impact, and understand what remains preserved?
5. Can a user distinguish an ordinary answer failure, a provider/authentication failure, and a memory-retrieval failure?
6. Can a user find a failed processing job, preview a candidate, and reject it without losing the audit receipt?
7. Does the redacted audit/metrics presentation communicate enough operational context without exposing content?

## Participants and safeguards

- Recruit 5–8 participants for the first formative round, with a mix of technical and non-technical users.
- Use a disposable local dataset with synthetic identifiers and clearly marked test content.
- Do not ask participants to enter passwords, API keys, real conversations, health/financial information, or personal secrets.
- Obtain consent for screen/session recording if recording is used; otherwise capture task outcomes and observations only.
- Reset the disposable database and browser profile between participants.

## Task protocol

Give participants only the task statement, not the expected control or route. Ask them to think aloud, but do not teach the interface during the first attempt. Record time to completion, success/failure, wrong turns, confidence (1–5), and the explanation they give for what happened.

| Task | Seeded state | Success criterion | Key measure |
|---|---|---|---|
| Ask a memory-aware question | One relevant and one irrelevant synthetic memory | Sends the question, identifies scope, and explains whether private memory was used | Working-state recognition ≤2 s; evidence comprehension |
| Inspect evidence | Answer with two evidence items and a trace | Identifies at least one source ID and opens the trace from chat | Trace discoverability; explanation accuracy |
| Correct a memory | Active memory with source provenance | Replaces the content, recognizes the old record is superseded, and finds the receipt | Completion; accidental-retention misunderstandings |
| Forget a memory | Active memory with source and replacement links | Reads impact, cancels once, then retracts intentionally and explains preservation semantics | Safe-action success; cancellation recovery |
| Recover from provider failure | Simulated unavailable provider | Reads the error, does not resend blindly, and finds retry/status guidance | Recovery success; duplicate-action rate |
| Review processing | Pending consolidation job with a candidate proposal | Previews, rejects or approves, and locates the action trace | Decision accuracy; receipt discoverability |
| Find an operational issue | Mixed successful/error traces and metrics | Filters errors or retrieval runs and describes latency/error state without exposing content | Filter success; privacy comprehension |
| Explain capture control | Chat with capture enabled and disabled turns | Correctly predicts which turn becomes durable memory | Capture-control comprehension |

## Scoring

Report task-level results, not just satisfaction:

- task completion rate and first-attempt completion rate;
- time to first visible working state and time to task completion;
- wrong-control rate, duplicate-submit rate, and accidental retraction rate;
- trace discoverability and source/provenance explanation accuracy;
- correct interpretation of scope, capture, supersession, and preservation semantics;
- recovery success after provider/auth/job failures;
- confidence before and after the task;
- qualitative privacy concerns and points of confusion.

Suggested formative thresholds: ≥80% first-attempt completion for core tasks, zero critical accidental deletion/retraction events, ≥80% correct interpretation of capture/scope semantics, and ≥80% trace discovery without moderator help. Treat these as study thresholds, not claims about the population.

## Procedure and analysis

1. Run a five-minute orientation using synthetic data only; do not explain the solution path.
2. Randomize task order where practical, while keeping the seeded state reproducible.
3. After each task, ask “What do you think happened?” and “What would you do next?” before correcting misconceptions.
4. Classify findings by severity: critical loss of user control/privacy, task blocker, misleading state, efficiency friction, or cosmetic issue.
5. Re-run the highest-severity tasks after each UX revision and compare task-level deltas.
6. Preserve anonymized notes and aggregate scores; do not retain participant-entered free text unless explicitly consented and synthetic.

## Relationship to automated evidence

Automated UX smoke, browser checks, simulator results, benchmarks, and integration tests validate deterministic paths and invariants. They do not establish learnability, comprehension, confidence, or real-world accessibility. The first human round is therefore a required publish-readiness gate rather than a replacement for the existing automated suite.
