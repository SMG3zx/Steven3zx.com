# vinci mark 3

Status: written 2026-11-18, after M0 to M5 of [mark 2](mark2-plan.md). It replaces mark 2's M5 remainder, M6 and M7. Everything in mark 2 that was built stays built; this plan says what to do next, what to stop doing, and what to measure before deciding.

Tags on claims: **[V]** measured or read in this repo. **[R]** from research, with the source in section 9. Most 2026 sources are blogs, vendor posts and aggregator pages, so [R] claims are leads to test, not facts. **[E]** an estimate.

## 0. The verdict in one page

Mark 2 built the plumbing: a Mojo core, a journal, a hybrid index, a chat, a portal, feeds. It did that in about three and a half days of wall time [V], against 15 working days estimated. So building is no longer the scarce resource. The scarce things are now **data about you**, **decisions**, and **a way to tell whether a change helped**. Mark 3 is organised around those.

1. **The COO has nothing to operate on yet.** Of 373 journal lines, about 100 come from you: 19 ideas with their 19 homes (one per repo), 28 `remember`, 25 chat exchanges (mostly demo questions), 4 bookmarks. The rest is tracker output (82 `work`, 15 `spent`) and 160 `memory` records imported from the old system's test data. There is no `goal`, `req`, `for`, `kept`, `missed` or `ship` line [V]. A COO that proposes needs commitments to propose about.
2. **So build the daily brief first, and use it.** A deterministic, cited brief, rated with one keystroke, for two weeks. Proposals come after, and only if the ratings and the data justify them.
3. **Stop improving retrieval.** It is saturated: any@10 98.9%, all@10 95.6% on LongMemEval [V]. Answer accuracy is 65 to 69% because of the model that reads, not the index. A reranker and an embedding fine-tune would work on the wrong stage.
4. **Attack the reader instead.** The chat model runs at about 8 tokens/s and answers temporal and multi-session questions at roughly 60% [V]. Five cheap experiments (thinking mode, whole-journal-in-context, multi-token prediction, a mixture-of-experts model with offload, more RAM) each have a decision rule (section 2.4).
5. **Mojo stays, for the right reason.** It is not faster here: numpy does the vector scan in 6.29 ms and the Mojo SIMD kernel in 6.3 ms at 100k × 768 [V]. M0 compared against pure Python, which was the wrong baseline. Mojo stays because the core is built and passing, you want to learn it, and strict compile feedback helps agents. It does not get new work that Python does better (section 2.2).
6. **Fix the measuring before the next tuning round.** The personal set can only detect changes of about 8 questions out of 48. Most of what mark 2 tuned (fusion weights, merge rules) moved one or two questions, which is noise. Larger sets, a calibrated judge, and one gate command come first.
7. **Security is Meta's Rule of Two, applied per surface** (section 2.6). The operator never holds a write capability beyond `proposal`.

## 1. What the evidence says about mark 2

| Mark 2 claim or estimate | What was measured | Verdict |
| --- | --- | --- |
| Rewrite about 4.5k lines | 16,562 lines of Mojo source, 5,786 of tests, 1,758 of Python edges, 3,727 of eval scripts, 3,336 of diff harness [V] | Scope grew about 3.7×. Cheap, because agents wrote it |
| 15 working days for M1 to M5 | M0 to M5d in about three and a half days of wall time (16 to 18 November) [V] | Estimates were far too high. Stop condition 2 never applied |
| Stop condition 1: 60% pass by the third try | 15 of 18 agent tasks passed `make check` by the third logged attempt (83%); 7 of 18 on the first (39%) [V]. Attempts are logged `make check` runs, not independent trials, and a few failures were harness bugs | Passes, with a wider margin than M0's 3-task sample |
| Mojo is fastest where it matters | numpy 6.29 ms vs Mojo 6.3 ms at N = 100k, D = 768. numpy 0.08 ms at the real size (3k × 2560) [V] | **Speed is not a reason to use Mojo here.** Learning and agent feedback are |
| Recall is the thing to build | any@10 98.9, all@10 95.6 (LongMemEval); answers 65/100 chat, 69/100 single-shot [V] | Retrieval is not the limit. Reading is |
| Feeds will improve answers | In the default search they lowered personal hits from 35 to 25 until journal-first merging. Vectors alone on feed questions 15/15 vs 13/15 fused [V] | Feeds are a separate source, asked for on purpose |
| Judge: the same 27B model | One clearly wrong verdict seen (`j-01`); research says self-preference is real but often small once quality is controlled [R] | Calibrate it by hand once (section 2.7) |
| A scorecard from the journal | The journal has no goals, commitments or brief ratings [V] | The data does not exist yet |
| Time tracking continues | The awatcher idle watcher wrote nothing from 17 November 05:26 UTC to 18 November 18:18 UTC, so about 33 hours of window time were dropped [V] | Feeds need a health check that says when they went quiet |

## 2. Each part, attacked

For each part: the question, the strongest attack on the current answer, what research and measurement say, and the verdict.

### 2.1 The purpose: is "a COO that proposes" the right product?

*Attack 1: it is a product with no input.* A COO runs commitments, and the journal holds none [V]. Proposals built on tracker lines and imported test data would be noise, and the plan's own section 6 says an interrupting assistant that is wrong gets switched off.

*Attack 2: three UIs (terminal, Waybar, web queue) before one habit exists.* You used chat on two days, 15 to 16 November [V]. A queue UI for a flow you do not yet use is the wrong order.

*Research.* Builders of personal "chief of staff" systems report the same thing: the assistant is only as good as what it is fed, so fix the notes first [R]. They start with a read-only morning brief before any automated action [R], and treat the approval queue as the core safety pattern, which almost no product ships [R]. Studies find that how much initiative people want varies by person, and that notification burden is the recurring failure [R]. A claimed budget of three to five unsolicited updates a day is a practitioner opinion, not a finding [R, weak].

*Verdict.* Keep the direction (you decide, it proposes). **Change the order:** goals in the journal, then a deterministic brief, then two weeks of ratings, then proposals. Delete the web queue for now (section 2.8). The brief's usefulness rating is the first scorecard number, and it exists on day one.

### 2.2 The language: Mojo first?

*Attack.* If speed is not the reason, why keep Mojo? Today, HTTP, SQLite, embeddings, the portal server and git reads all go through Python [V]. Mojo is the glue and the kernels, and the kernels are not needed (numpy equals them at this scale).

*What is left.* Three reasons, all stated by you in the interview: learn Mojo, own the stack, and train models later. Plus a measured one: agents pass the compile loop at 83% by the third try, and the 16.5k lines pass 188 tests and four differential suites [V]. Switching now would cost more than staying.

*Risk check [R].* Mojo 1.0 shipped on 11 August 2026 and Qualcomm closed its purchase of Modular on 29 July 2026. The compiler's opening is promised for 2026. I found no source that confirms Mojo GPU support for the consumer RTX 3060 (Ampere). So **Mojo GPU kernels are not planned** until a release note says it works. The pin (Mojo 1.2.0) holds.

*Verdict.* Stay, with a rule: **Mojo for vinci's own logic and anything you write to learn; Python (strict, stdlib or numpy) for edges and for any new compute where a library already does it.** Do not re-port Python edges to Mojo. The generated .NET HTML-entity tables (about 600 lines) can be deleted once golden files replace the C# oracle (section 2.9). The learning goal needs a real track (section 3, K6): agents wrote all the Mojo, so reviewing is not learning.

### 2.3 Memory and recall: what is left to build?

*Attack.* Everything measured says stop. Recall is saturated, the fusion weights moved one question, and the audit found two eval harnesses silently returning nothing after a default changed [V].

*Research.* Vendor numbers on LongMemEval are higher (Mem0 reports 94.4 at about 6.8k tokens per query), but judges and budgets differ, so they are not comparable [R]. A harder successor benchmark, LongMemEval-V2, exists, and frontier models score at most 14% on it without trajectory evidence [R]. Off-the-shelf Qwen3 rerankers did worse than a purpose-trained memory reranker, and a trained one only reached MAP 0.80 over a dense first stage [R]. Guides advise fine-tuning an embedder only if recall at the depth you feed a reranker is still low [R]. Ours is 95.6% all@10.

*What would still help.* The failures are temporal reasoning (19/30) and multi-session (17/30) [V]. These need the reader to count, order and compare across records. That is a reading problem (section 2.4), and it is where test-time compute helps [R: MemCoT reports 88 on LongMemEval-S by iterative memory search; I could not verify its model or setting].

*Verdict.* **Freeze retrieval.** No reranker, no embedding fine-tune, no new fusion work. Keep: journal-first merge, `--sources`, 0.25:1 semantic fusion. Delete candidates from section 6. One new test instead: **can the memory system be deleted at today's size?** The whole journal is about 18k tokens, which fits in the 32k context. If whole-journal-in-context matches recall on the personal set, retrieval only earns its keep once the journal outgrows the context. This is experiment E2.

### 2.4 The reader: model, speed and hardware

*Attack.* "Make 12 GB work" was a stated goal, but the current setup gives about 8 tokens/s and 65% on LongMemEval, and every answer takes 8 to 12 seconds [V]. The most useful lever is the one mark 2 treated as fixed.

*Research [R, mostly community reports and aggregator sites; every number must be re-measured here].*

| Option | Claim | Fit for this machine (RTX 3060 12 GB, 30 GB RAM) |
| --- | --- | --- |
| Multi-token prediction (MTP) in llama.cpp mainline | 1.7× on a dense Qwen 3.6 27B; no speedup on MoE | Needs a GGUF with the MTP head. Cheap to test if one exists for our model |
| 30B-class MoE with expert offload (`--n-cpu-moe`) | About 32 to 43 tok/s for Qwen3.6-35B-A3B at Q4 on a 3060 | Needs about 24 GB of RAM. About 8 GB of RAM is free while the desktop runs, so it fits only with the desktop closed (overnight). Neo4j uses only about 0.45 GB, so stopping it does not help |
| Qwen3.8-Flash-Next on Strata | 125B total, about 6B active. 94 tok/s on a 12 GB RTX 5070 with **64 GB RAM**; 11 to 15 tok/s with 32 GB, because experts stream from SSD | **The headline setup does not fit at 30 GB, and the RAM upgrade is ruled out (D2).** You still want it tested, so E5 below tests what fits |
| Thinking mode on the current model | Test-time compute helps long-memory reasoning in research, but I found no direct LongMemEval ablation | Our server disables it today (`enable_thinking` false). Free to test |

*Verdict.* **Run the model lab (K3) before any more chat-side work.** Five experiments, each with a rule fixed in advance:

- **E1, thinking mode:** answer LongMemEval and the personal set with thinking on. Adopt for temporal and multi-session routes if it gains at least 8 points at no more than 3× the time.
- **E2, whole journal in context** (the delete test from 2.3).
- **E3, MTP:** same outputs at temperature 0, tokens/s measured. Adopt if at least 1.4× and identical or better accuracy.
- **E4, a MoE model with offload:** tokens/s, accuracy, RAM headroom. Adopt if accuracy is not lower and speed is at least 2×.
- **E5, a Strata test** (replaces the cancelled RAM upgrade): install a pinned release in a contained directory, serve a model that fits in 12 GB VRAM and the free RAM, and measure it like E4. Which model and quant comes from the research pass (the report is tracked in section 3, K3). The headline Flash-Next setup needs about 64 GB and is not testable here.

All use the existing harnesses (`budget_sweep`, `chat_eval`) and 500 LongMemEval questions, not 100 (section 2.7). GPU windows are scheduled when you are not using chat; each model swap takes the chat server down for about 10 minutes.

### 2.5 Ingestion: feeds, mail and calendar

*Attack.* The feed contract is sound, but three of seven planned feeds have no evidence of value, and every feed adds an untrusted input and a thing that can go quiet (the idle watcher went silent for 33 hours and nobody was told).

*Research [R].* The pattern that holds up for agents is **read-only by construction**: mbsync mirrors mail one way into a local maildir, notmuch indexes it, and the agent process has no way to send or delete, because the code path does not exist. vdirsyncer and khal do the same for calendars. Filters belong in code, not in the prompt, and ingested text is wrapped as data.

*Verdict.* Keep the contract. **Order by what the brief asks for:** journal, git, vaults (done), then **calendar** (an `.ics` mirror is small and answers "what is today"), then mail only if rated briefs show missing mail. Skip screen text and browser history. Every feed gets a health line in `vinci feed status` ("last event 33 h ago") and the brief says when a source is quiet. Activity stays off by default and out of default search.

### 2.6 Security: the Rule of Two, per surface

Meta's Rule of Two [R]: until injections can be reliably detected, one session should hold at most two of (processes untrusted input, accesses sensitive data, changes state outside). CaMeL-style designs, where the model proposes and a deterministic policy decides, are the structural option [R]. Filters do not hold: human red-teamers bypassed all 12 defenses in one study [R].

| Surface | Untrusted input | Sensitive data | Changes state | How it holds |
| --- | --- | --- | --- | --- |
| Chat | yes (feeds, read pages) | yes (journal) | journal writes | **State change is gated by a human y/N once untrusted text is in the context** [V]. Audit every write path (the `read` tool can fetch any local web address) |
| Operator (new) | yes (feeds, mail) | yes | only `proposal` and `brief` lines | The operator process can only append those two kinds. It cannot accept. Enforced in code, with a test |
| Accepting a proposal | no | yes | yes | Human only: terminal or an authenticated, loopback-only request |
| Portal | no | yes | no | Read-only, loopback, Host check [V] |
| Feeds | yes | no | index only | One-way mirrors; no credentials for sending anywhere |
| Claude Code reading vinci | no | yes | no | **Data leaves the machine to the API provider.** Off unless you turn it on (decision D3) |

### 2.7 Judging: how to tell whether a change helped

*Attack.* The personal set is 60 questions (48 answerable). A 95% interval on a hit rate near 60% at n = 48 is about ±14 points [E]. A paired change must flip about 8 net questions to be significant [E]. Fusion and merge tuning moved 0 to 1 questions. The judge is the answering model, so its errors correlate with the answerer's.

*Research [R].* Self-preference bias exists, but newer controlled studies find much of it disappears when answer quality and judge severity are controlled. The advice is to blind the source and check the judge on ground-truth tasks.

*Verdict.*
1. **Calibrate the judge once.** You label 40 answers (about 20 minutes). Report judge agreement with you. Use the lexical check as the second signal wherever a ground-truth string exists.
2. **Bigger sets for decisions.** Full LongMemEval-S (500) for reader changes, about 70 minutes per configuration overnight. A personal v3 of at least 150 questions built from the journal once it holds real content.
3. **Every eval table carries a minimum detectable difference.**
4. **The scorecard.** Brief rating (1 to 5, on day one), then proposal precision and recall, commitments kept, cycle time, answer quality. Pick three after a month of data, as mark 2 said.

### 2.8 Interfaces

Terminal first. A Waybar module needs one JSON status file written by the brief timer (text, tooltip, class) and costs about 3 hours [E]. **The web queue is deleted from the plan** until you say you do not look at the terminal or Waybar when proposals arrive. It adds POST surface and a CSRF defense for a flow that does not exist. The portal stays read-only.

### 2.9 How the work gets done: agents, gates and safety nets

*Attack.* The regression gate depends on a .NET runtime and a folder outside the repo. The journal has no automated backup, and two writers can mint the same id. Evals are hand-run and their numbers live in prose.

*Verdict.* This is K0, and it comes first. It includes: golden files from the C# oracle (then the oracle can retire), `make gate` (check, goldens, eval thresholds, one summary line), `vinci backup` and `vinci verify`, a lock around next-id plus append, a fresh-clone test, and fixing the doc errors the audit listed. The agent team stays Haiku-written and Opus-reviewed: 83% pass by the third try, and the failures cost minutes, not days [V].

### 2.10 Models and fine-tuning (was M7)

*Attack.* There is nothing to tune on yet. Proposal accept/reject labels do not exist. The weakest measured stages are the reader (model choice, E1 to E5) and the missing data, not tool use (every chat conversation used recall [V]) and not retrieval.

*Research [R].* Fine-tuning an embedder with a few hundred pairs often overfits; try a stronger base, hybrid search and a reranker first. QLoRA fits 7B to 14B on 12 GB.

*Verdict.* **Fine-tuning is deleted from the near plan.** It returns when there are at least 200 labeled proposals and one stage is the measured weakest. Routing between two models (small for tools, large for reasoning) waits for E3 and E4.

## 3. The phases

Estimates are agent work plus your review, from this project's own rate [V]. Anything that needs real days of use (K2) is elapsed time, not effort.

| Phase | Does | Done when | Effort |
| --- | --- | --- | --- |
| **K0: clean and gate** | The audit's delete list; golden files; `make gate`; `vinci backup`/`verify`; append lock; write-path audit; LME harness fix; fresh-clone test; doc fixes; `.gitignore` | `make gate` passes on a fresh clone with no .NET and nothing outside the repo; the 342-line journal round-trips through backup and verify | 1 day |
| **K1: goals** | **You** enter commitments for your top 3 to 5 ideas (`for`, `goal`, `req`, deadlines) and answer a short interview about a normal day. An agent helps draft. | The journal has real commitments the brief can cite | 1 hour of yours |
| **K2: brief v0** | `vinci brief` (morning, evening): ideas state, what is stuck, time and commits since last time, feed health. Deterministic, every line cites an id. A systemd timer, a Waybar status file, a 1-to-5 keystroke logged as a `brief` line. An LLM-written variant is added as an A/B arm after a week. | 10 rated briefs. Median rating at least 3. If not, the operator only speaks on request (mark 2, section 8) | 1 to 2 days to build, 2 weeks to run |
| **K3: model lab** | E1 to E5 with their decision rules, and a research pass first on the best model for this hardware. One combined set of overnight GPU windows, with the desktop closed for the RAM-heavy tests. Runs in parallel with K2 | Each experiment has a recorded adopt-or-drop decision on the 500-question set | 2 to 3 days, GPU time overnight |
| **K4: proposals** | Journal kinds `proposal`, `accept`, `reject`, `dismiss`, `missed-proposal`; the operator on a timer (at most 3 proposals a day to start); the scorecard command; the Rule of Two enforced in code | A month of precision and kept-rate data | 3 days to build, a month to run |
| **K5: calendar, then mail** | An `.ics` feed, read-only, with a quiet-source check. Mail only if briefs show a gap. | Calendar items appear in the brief; second run adds nothing | 1 day each |
| **K6: learn and train** | A Mojo module **you** write with an agent as tutor (the first candidate is a small reranker or scorer, since only a user-written kernel serves the learning goal). Fine-tuning only after the data gate in 2.10 | You can explain and change the module; a measured gain or the change is reverted | Open |

## 4. Stop conditions for mark 3

- **K1 not done after a week** means the COO is premature. vinci stays a memory-and-notes tool plus the brief, and the operator is not built.
- **Median brief rating under 3 after 10 briefs:** the brief stops being pushed; it is generated on request.
- **No model-lab experiment clears its rule:** the reader stays as is, and the plan stops spending on speed.
- **Proposal precision under 50% after four weeks of tuning:** the operator speaks only in the brief (mark 2 section 8).
- **A Mojo release breaks the pinned build twice in a quarter:** unchanged from mark 2.
- **Qualcomm changes Mojo's terms:** the pin holds; revisit at the next quarterly review.

## 5. Decisions only you can make

| # | Decision | Default if you do not decide |
| --- | --- | --- |
| D1 | Your commitments and deadlines for K1 | K2 waits |
| D2 | A RAM upgrade to 64 GB | **Decided 2026-11-18: no.** The hardware is fixed at 12 GB VRAM and 30 GB RAM |
| D3 | Whether Claude Code may query vinci's memory (this sends results to Anthropic's API) | **Decided 2026-11-18: no, local only** |
| D4 | Whether mail ever comes in, and from which account | Not before K5 |
| D5 | Whether to keep the old C# oracle after golden files exist | **Decided 2026-11-18: archive for a month after `make gate` passes on a fresh clone, then delete** |
| D6 | Neo4j: stop the container | **Decided 2026-11-18: ignore for now.** It uses about 0.45 GB of RAM; it still listens on all network interfaces |

## 5b. Decisions made in the grilling session (2026-11-18)

1. The target is a COO that runs your commitments, with the one-week goals rule as the exit (section 4).
2. K0 is approved. The two untracked files (the `.bak` with Neo4j keys, `portal-ci.yml`) are listed for deletion but the automatic permission check blocked removing them, so they wait for an explicit go-ahead.
3. Neo4j keeps running for now (D6).
4. The model lab is one combined set of overnight GPU windows, including a Strata test within the hardware that exists, plus research on the best model for it.
5. No RAM purchase (D2).
6. Claude Code does not query vinci's memory (D3).
7. You will hand-label 40 answers (about 20 minutes) after K0, to calibrate the judge.
8. Haiku writes implementation and Opus reviews; Sonnet or Opus may do research, audits and planning.
9. `AGENTS.md` records the temporary rule: push straight to `github`, Gitea paused.
10. The C# oracle retires a month after the gate passes on a fresh clone (D5).
11. You will spend about 2 hours a week writing Mojo yourself, starting after K0 (K6).

## 6. What to delete and what stays

From the audit and this review, in order of safety:

- **Delete now:** `tune_results/scratch/memory.env.bak` (holds the Neo4j password keys; now git-ignored, deletion pending approval); the untracked `.gitea/workflows/portal-ci.yml` (points at a folder that is gone); `not_ported` and its exit-code branch; the stale `deploy/memory.service` header line in `~/.config/vinci/memory.env`.
- **Delete after K0:** the C# oracle and `portal_truth/` with the generated entity tables (about 600 lines, replaced by golden files); `mojo/calibration/` (tag first; keep `attempt.sh` and the attempts-log format, because agent pass rate is a measure we still use).
- **Delete from the plan:** the web queue, mail before evidence, activity text in default search, a reranker, an embedding fine-tune, Mojo GPU kernels, the summarize step.
- **Stays, though it looks deletable:** `mojo/py/vinci_diff/` until goldens exist; `docs/mojo-gotchas.md`; the eval question files; the write gate; `Swift-Qwen3.8-27B_GGUF.py` (the 12 GB tuner, needed again by K3); the `memory` removed-command stub.

## 7. What would change this plan

- **The brief is rated well and you ask for more:** proposals move up, and K5 (calendar) moves ahead of K3.
- **E1 or E4 shows a large reader gain:** answer quality stops being the main risk, and the operator can lean on the chat model for judgments.
- **E2 shows whole-journal-in-context matches recall:** retrieval is shelved until the journal passes about half of the context window, and the index becomes a feed-only tool.
- **You do not enter goals:** the product is a memory tool, and the COO language leaves the plan.
- **Mojo GPU support arrives for Ampere, and a kernel you write beats numpy at a real size:** reopen Mojo kernels.

## 8. Sources

Tags: P = paper or official post; V = vendor claim; B = blog or aggregator (weak).

- Memory benchmarks and systems: [State of AI Agent Memory 2026 (V)](https://mem0.ai/blog/state-of-ai-agent-memory-2026), [LongMemEval-V2 (P)](https://arxiv.org/pdf/2605.12493), [MemCoT (P)](https://arxiv.org/pdf/2604.08216), [MemReranker (P)](https://arxiv.org/pdf/2605.06132)
- Fine-tuning and reranking guidance: [practical guide to fine-tuning embedding models (B)](https://blog.lancedb.com/a-practical-guide-to-fine-tuning-embedding-models)
- 12 GB local models: [Best local LLMs for RTX 3060 12GB (B)](https://www.llamabuilds.ai/gpus/rtx-3060-12gb/best-models), [RTX 3060 Reddit consensus (B)](https://specpicks.com/reviews/rtx-3060-12gb-local-llm-reddit-consensus-2026)
- Strata and Flash-Next: [Strata repository](https://github.com/Niko1221/Strata), [which quant to run, 16 to 128 GB (B)](https://pub.towardsai.net/qwen3-8-flash-next-on-strata-which-quant-to-run-from-16gb-to-128gb-in-2026-843778d218a1), [the 32 GB number nobody measured (B)](https://insiderllm.com/guides/qwen3-8-flash-next-rtx-3090-3060-32gb/)
- Multi-token prediction: [llama.cpp MTP boosts Qwen3 coding speed (B)](https://aiweekly.co/alerts/llamacpp-mtp-boosts-qwen3-coding-speed-71), [run Qwen 3.6 MTP in llama.cpp (B)](https://mer.vin/2026/05/run-qwen-3-6-mtp-in-llama-cpp-faster-local-inference-with-built-in-speculative-decoding/)
- Mojo: [Mojo 1.0 and Modular 26.5 (P)](https://modular.com/blog/modular-26-5-mojo-1-0-is-here), [Qualcomm and Mojo (B)](https://www.jonpeddie.com/news/qualcomm-opens-mojo-to-challenge-cuda/), [Mojo roadmap (P)](https://mojolang.org/docs/roadmap/)
- Security: [you cannot filter your way out of prompt injection (B)](https://hackernoon.com/you-cannot-filter-your-way-out-of-prompt-injection), [the lethal trifecta and blast radius (B)](https://www.sophos.com/blog/inside-the-lethal-trifecta-blast-radius-reduction-in-ai-agent-deployments)
- Read-only email for agents: [let Claude search your email without sending or deleting (B)](https://hackernoon.com/how-to-let-claude-search-your-email-without-letting-it-send-or-delete)
- Proactive assistants and chiefs of staff: [background agents and the notification budget (B, weak)](https://tianpan.co/blog/2026/05/13/background-agents-notification-budget-attention-economy), [Edwin](https://github.com/bwelker/edwin), [claude-chief-of-staff](https://www.sourcepulse.org/projects/24805494)
- Judge bias: [Self-preference bias in LLM-as-a-judge (P)](https://arxiv.org/abs/2410.21819)
- Mark 2 and the memory plan: [mark2-plan.md](mark2-plan.md), [plan-one-journal-one-index.md](plan-one-journal-one-index.md), [m3-results.md](m3-results.md), [m4-results.md](m4-results.md), [feeds.md](feeds.md)
