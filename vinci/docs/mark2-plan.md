# vinci mark 2

Status: M0, M1 and M2 done (2026-11-16): continue in Mojo; the core runs in Mojo and matches the C# binary ([m2-results.md](m2-results.md)). M3 is next.

Mark 2 is three decisions and one direction:

1. **Language.** vinci moves from C# to Mojo, with Python libraries called from Mojo where Mojo has none. The move stops, and falls back to Python, if the measured checks in section 3 fail.
2. **Memory.** One journal as the store of record and a SQLite index, as in [plan-one-journal-one-index.md](plan-one-journal-one-index.md). That design is now built in Mojo, not in C#.
3. **Ingestion.** Every outside source comes in through one contract (section 5).
4. **Direction.** vinci becomes your chief operating officer: it runs operations, proposes, and you decide (section 4).

## 1. What you said (interview, 2026-11-16)

| Question | Answer |
| --- | --- |
| Why leave C#? | All four: Python syntax, AI writes better Python, raw speed, Python's ML ecosystem |
| Who is vinci for? | Only you |
| Who writes the code? | Agents; you review |
| Where does it run? | This Linux box (12 GB GPU) |
| Where is speed needed? | Search and embeddings, and headroom for later |
| Train models? | Yes, a goal |
| Learn Mojo? | Yes, strongly |
| Cloud models? | Local only |
| The shape of the code | **Mojo first and foremost.** Train the local model to be better at Mojo if needed. Fall back to Python if it does not work. |
| Strictness | Strict types, gated by the hooks |
| In a year | "My own J.A.R.V.I.S.: a super intelligent, all-knowing partner that is my COO, and I am the CEO" |
| What it runs | Your projects, your time and days, learning and research, life admin |
| Autonomy | It proposes, you approve |
| Speaking first | Yes, a daily brief |
| Interface | Terminal chat, a desktop panel, a web page |
| Hardware | Make 12 GB work |
| What it reads | Repos and git, notes and docs, email and calendar, screen and activity, and more; better ingestion |
| How to judge it | To be researched (section 6) |
| What to fine-tune first | Decide by measuring |
| Relationship to janus | Stays separate |
| Churn and breakage | A rewrite is fine, and anything may break for a while |

## 2. The language, from first principles

### Delete first

**Can the language question be deleted?** For speed, yes. Today the time goes to the model in llama-server (C++ and CUDA): seconds per answer. vinci's own code is a small share of it. Recall measured about 0.5 s, and most of that is Neo4j, HTTP and the embedding call, which the memory plan removes. No host language changes that.

**It cannot be deleted for the other reasons.** Learning Mojo is a goal you stated, and for a personal tool your skill and enjoyment count as benefits. Training models is a goal too, and that is where compiled kernels and the Python ML stack matter.

### Ground truths

1. **The model is the bottleneck, and it runs in llama.cpp.** The host language moves latency by a few percent at most, until vinci runs its own kernels (vector scans at scale, a reranker, training).
2. **Agents write the code and you review it.** What matters is how often an agent's first attempt compiles and passes the tests, and how easy the diff is to read.
3. **A strict compiler is feedback an agent can use.** Mojo and C# have one. Python needs pyright in strict mode to get close.
4. **Models know little Mojo.** Few public Mojo programs exist, and the syntax changed a lot before 1.0 (August 2026), so most of what a model learned is out of date. MojoBench (NAACL 2025) found that GPT-4o and Claude 3.5 Sonnet did poorly at Mojo, and that Claude sometimes wrote Python when asked for Mojo. No published study compares the same models on Mojo and Python directly.
5. **Mojo calls Python.** `Python.import_module` reaches any Python library in CPython. Each call across that boundary has an overhead, so it belongs at the edges, not in tight loops.
6. **One user, one machine.** Packaging, other operating systems and contributors cost nothing here.
7. **The journal is plain JSON lines.** Any language reads it, so the data needs no migration.
8. **12 GB limits training.** QLoRA fine-tuning fits 7B to 8B models comfortably and 14B tightly. A 27B model like today's chat model needs about 24 GB, so it cannot be fine-tuned on this card.

### Cost and benefit

| | Stay in C# | **Mojo first, Python at the edges** | Python, Mojo for kernels |
| --- | --- | --- | --- |
| Migration cost | None | Rewrite about 4.5k lines: the 3.8k outside `Memory/`, plus recall and ranking. The other 8k lines of `Memory/` are deleted by the memory plan | Same rewrite |
| Agents writing it | Good: a large corpus and a strict compiler | **Weakest**: a small, outdated corpus. Helped by Modular's skills, a pinned version and the compiler loop | Best: the largest corpus, made strict with pyright |
| Your enjoyment, and learning Mojo | Low | **Highest** | Medium: Mojo only in the hot paths |
| Speed of vinci's own code | Fast (JIT, SIMD through `TensorPrimitives`) | Fastest where it matters (SIMD, GPU) | Slow in plain Python, fast in its Mojo parts |
| ML ecosystem (training, tokenizers, rerankers) | Poor | Through Python, with a boundary cost | Native |
| Libraries (SQLite, HTTP, mail, calendar, TUI) | Mature | Few in Mojo (community `sqlite`, `flare`, `lightbug_http`, `EmberJson`); the rest through Python | Mature |
| Tooling risk | Low | **High**: 1.x is months old, compiler contributions are closed until late 2026, and Modular now belongs to Qualcomm | Low |
| Way back if it fails | — | To Python: the edges already are Python, and Mojo syntax is close | — |

**Recommendation: go Mojo first, as you asked, but treat it as a bet with a stop condition, not a belief.** Its two weak points are measurable early: how well agents write Mojo, and whether Mojo's libraries are good enough. Phase M0 (section 7) measures both before anything depends on them. The fallback is cheap, because the parts that touch Python libraries are written against Python from the start.

### Stop conditions

Fall back to Python, and keep only the kernels in Mojo, if any of these holds after phase M0, or again at any later phase:

- On the calibration tasks, fewer than **60%** of agent changes compile and pass their tests by the third try, even with Modular's skills and the docs installed.
- A phase takes more than **twice** its estimate because of Mojo itself (missing features, compiler bugs, breaking releases), not because of the design.
- A Mojo release breaks the build twice in one quarter while the version is pinned.

### Making agents good at Mojo

In order of cost. Each is measured on the calibration tasks before the next is added.

1. **Pin the version** with uv and the PyPI `mojo` package, pinned exactly in `vinci/mojo/pyproject.toml`, and update it on purpose, not by accident.
2. **Modular's own skills** (`mojo-skills@modular` in Claude Code) and the Mojo docs MCP server (`mojo-mcp.modular.com`) for the agents. This is development tooling, so local-only does not apply. Only vinci's data must stay local.
3. **The compiler and tests in the loop.** `mojo build` and `mojo test` run in a hook after each edit, so an agent sees its errors immediately.
4. **`docs/mojo-gotchas.md`**, kept the way vinci keeps principles: each mistake an agent makes twice becomes one line with a correct example, and agents read it first.
5. **Fine-tuning, last.** Tuning a 7B to 14B coding model on Mojo's standard library and vinci's own code is possible on 12 GB. Qwen 27B is not. Measure it with HumanEval-Mojo against the agents above. Claude and Codex write vinci's code, so a tuned local model only helps if it becomes one of the agents.

### Python at the edges, strictly

Python code is written as modules that Mojo imports. `pyright --strict` and `ruff` run in the pre-commit hook, so untyped code does not get in. The same gate runs `mojo format`, `mojo build` and the tests.

## 3. What carries over and what does not

| Today (C#) | Mark 2 |
| --- | --- |
| `Journal.cs`, `State.cs`, `App.cs`, `Review.cs`, `Stats.cs` | Mojo. **The C# binary is the oracle:** both run on the same journal, and their outputs must match line for line (a differential test) |
| `Tracker.cs` (ActivityWatch, git) | Becomes two feeds (section 5) |
| `Portal.cs` | The web page (section 4), served by Mojo (`flare` or `lightbug_http`), or by Python if those fall short |
| `Chat/` | Mojo, talking to llama-server over HTTP |
| `Memory/` (Neo4j, the API, the worker, the queue, about 8.5k lines) | Not ported. Replaced by the index in the memory plan, built in Mojo |
| `Memory/Evaluation/` | Python. The evaluation scripts in `~/.vinci/eval` already are, and they gate every phase |
| `~/.vinci/journal.jsonl` | Unchanged. It is the contract between the old vinci and the new one |

Changes to the memory plan:

- Phase 0 (the evaluation gate) is unchanged and comes first. It does not depend on the language.
- Phases 1 to 3 are built in Mojo. The index is not built in C# first.
- The vector scan is a Mojo SIMD kernel, the first piece of real speed work and a good first Mojo lesson. Measure it against `TensorPrimitives` in C# on the same vectors.
- SQLite: the community `sqlite` package if it covers FTS5, otherwise Python's `sqlite3` through interop (one call per query, so the boundary cost is small).

## 4. Where vinci goes: a COO that proposes

A COO runs the operation and brings the CEO decisions, not tasks. Your answers set the limits: it proposes and you approve, it runs locally on 12 GB, and it speaks first with a daily brief.

### The parts

```
 sources ──► feeds ──► index ◄──── journal (what you said and decided)
                         │
                    recall + state
                         │
              ┌──────────┴──────────┐
          the chat            the operator (runs on a timer)
       (you ask)            proposals, daily brief, warnings
              └──────────┬──────────┘
                 you approve or reject
                         │
                   journal (decision)
```

- **Proposal** is the COO's unit of work: a new journal kind with what, why, the evidence ids, and the action it would take. You answer `accept`, `reject` or `dismiss`, and that answer is a journal fact too. Nothing outward happens without an accept. This is also the main defense against prompt injection from email and web pages: untrusted text can at most cause a proposal you can see.
- **The operator** runs on a systemd timer. It reads state and the index and writes proposals and the brief. It calls no tool that writes, other than `propose`.
- **The daily brief**: in the morning, your commitments, what is stuck, today's calendar, mail that needs you, and the top three proposals. In the evening, what happened, what to answer, and the kept and missed list. It is one page, and every line cites an id.
- **Interfaces**: the terminal chat as today. A desktop panel: a Waybar module that reads one JSON status file (pending proposals, the current commitment, the next calendar item). A web page: the portal grows a brief and a proposals queue with accept and reject buttons.

### Models on 12 GB

You chose to make 12 GB work and to decide by measuring what to fine-tune. That gives an order:

1. **Measure where answers fail**: the evaluation sets, plus the proposals you reject, each with its reason.
2. **Route by task.** A small fast model (4B to 8B) for tool calls and classification, the 27B for reasoning and the brief, and embeddings on the CPU as today. llama-server can serve two models on two slots or ports.
3. **Fine-tune whatever is weakest**, with QLoRA up to about 14B. Accepted and rejected proposals are labels you produce anyway, so the operator collects its own training data. Retrieval (embeddings or a reranker) is the cheapest to tune and the easiest to measure.
4. **Mojo kernels where a profile shows a hot path**: the vector scan first, then a reranker, then training pieces. Later, measure whether Modular's MAX can serve the models in place of llama-server.

## 5. Ingestion: one contract for every source

### First principles

- **Delete first:** do not write syncers. Mature Linux tools already mirror each service to local files: `mbsync` (IMAP to Maildir), `vdirsyncer` (CalDAV to `.ics` files), git itself, ActivityWatch's local database, and Markdown folders. vinci reads local files and never talks to a mail or calendar server itself. That keeps it local-only, and keeps the credentials out of vinci.
- **Two kinds of truth.** The journal holds what you said and decided. Source mirrors hold what the world said. The index derives from both and can be rebuilt from both. Ingesting a source does not write into the journal, except for facts you make about it (a note, a decision, a proposal answer).
- **Everything ingested is untrusted.** Every record carries `trusted: false` unless it is your own writing.

### The contract

A feed is a small module with three functions:

```
cursor()                  where this feed got to last time (a byte offset, a commit, a timestamp, a file hash)
read(since) -> records    new or changed records since that cursor
record = {source, id, at, kind, title, body_path, hash, trusted, links}
```

The index stores each record by `(source, id)` and its hash, so a second read of the same thing changes nothing. A changed hash is a new version, never an edit. These are the journal's rules, applied to the outside world.

### Feeds, in order of value per effort

| Feed | Reads | Notes |
| --- | --- | --- |
| journal | `~/.vinci/journal.jsonl` | The only trusted feed |
| git | The monorepo's commits and diffs | Already partly done by `Tracker.cs` |
| vaults | Markdown folders (Meingrad, Research, docs) | Headings as sections, as atlas does it |
| activity | ActivityWatch | Window titles, beyond time counting |
| mail | Maildir from `mbsync` | Headers and plain text. Attachments as links only, at first |
| calendar | `.ics` from `vdirsyncer` | Events with times, so the brief knows your day |
| Linear | Your Linear issues | Optional. It is a cloud service, so reading it means sending a query out. Decide later |

Open: the order of the last four, and whether chat exports, browser history or PDFs come next. A short interview once the first three feeds work.

## 6. How to judge the COO

The research on proactive assistants measures them the same way: a proposal you accept is a true positive, one you reject or dismiss is a false positive, and **precision** (how often it is right when it speaks) matters most, because an assistant that interrupts badly gets switched off (Proactive Agent, 2024). ProAgentBench (2026) adds **recall**, things it should have raised, and uses F1 to weigh the two. A field study of a workplace agent found that unwanted interruptions were the main risk.

Proposed scorecard, every number from the journal:

| Measure | How | Why |
| --- | --- | --- |
| **Proposal precision** | accepted ÷ (accepted + rejected + dismissed), per week | Is it worth listening to? |
| **Proposal recall** | In the weekly review: "what did you do that vinci should have proposed?", logged as `missed-proposal` | Does it see enough? |
| **Commitments kept** | kept ÷ (kept + missed), as today | Does the operation run? |
| **Cycle time** | Seed to Tend, as today | Do ideas ship faster? |
| **Answer quality** | Hit rate and abstention on the evaluation sets | Is what it knows right? |
| Brief usefulness | One keystroke, 1 to 5, on each brief | Cheap and subjective. A check on the others |

Pick three as the main ones after a month of data. Until then, all are recorded and none is a target. A goal without a number cannot be checked, but a number chosen before there is any data is a guess.

## 7. The phases

Each phase ends with a check. Agents do the work, and you review at each check.

| Phase | Does | Done when |
| --- | --- | --- |
| **M0: the bet** | Install Mojo with uv (the PyPI `mojo` package, pinned in `vinci/mojo/pyproject.toml`), Modular's skills and the docs MCP. Hooks for format, build, test and pyright. Three calibration tasks: read the journal and rebuild state; SQLite with FTS5; a SIMD cosine scan. Record first-try and third-try pass rates, time, and every gotcha. From those, estimate how long each later phase should take. | The stop conditions in section 2 are checked and written down: continue, or fall back to Python |
| **M1: the gate** | Phase 0 of the memory plan: a harder personal set (about 60 questions), a fixed 100-question LongMemEval subset, and a baseline. Move `AUDIT_FILE` out of `/tmp`. | The baseline and dataset hashes are in `docs/memory.md` |
| **M2: the core** | Journal, state, commands, review and stats in Mojo. A differential test against the C# binary on the real journal and on generated ones. | Every command's output matches C# |
| **M3: memory** | Phases 1 to 3 of the memory plan in Mojo: new journal kinds, the index, recall, handles, `open`, the budget sweep. | At least the M1 baseline, with fewer tokens per answer |
| **M4: chat, then delete C#** | Chat in Mojo against llama-server, with citation checks and the write confirmation. Then remove the C# project, Neo4j and its units. | `vinci` is Mojo only, and the evaluations pass |
| **M5: feeds** | The feed contract. Journal, git and vaults first, then activity, mail and calendar. | Each feed is idempotent: a second run adds nothing |
| **M6: the COO** | Proposals, the operator on a timer, the daily brief, the Waybar panel, the web queue. | A month of precision and kept-rate data |
| **M7: models** | Routing between two models, then fine-tuning whatever the data shows is weakest, then Mojo kernels where profiles point. | A measured gain on the scorecard, or the change is reverted |

## 8. What would change this plan

- **M0 fails the stop conditions.** Python becomes the host, Mojo keeps the kernels, and the phases stay the same.
- **Mojo's SQLite or HTTP falls short.** Use Python's through interop. This changes one module each, not the plan.
- **Qualcomm changes Mojo's terms or pace.** The pin keeps vinci building. Revisit at the next quarterly review.
- **Proposal precision stays low after tuning.** Then the operator speaks less: only the brief, and no warnings between briefs.
- **12 GB caps quality** on the scorecard after routing and tuning. Then the hardware question comes back, with numbers.

## 9. Sources

- Mojo 1.0 and the open compiler: [Modular](https://www.modular.com/blog/mojo-open-source), [Wikipedia](https://en.wikipedia.org/wiki/Mojo_(programming_language)), [Linuxiac](https://linuxiac.com/mojo-programming-language-goes-fully-open-source/)
- Mojo and Python interop: [Calling Python from Mojo](https://docs.modular.com/mojo/manual/python/python-from-mojo), [Calling Mojo from Python](https://mojolang.org/docs/manual/python/mojo-from-python/), [forum: the cost of crossing](https://forum.modular.com/t/performance-cost-of-importing-python-module/2022)
- Mojo libraries: [packages](https://mojolang.org/packages/), [json, sqlite, flare and others on 1.0](https://forum.modular.com/t/flare-json-sqlite-mozz-morph-uuid-pprint-envo-tempo-and-mojodoc-are-update-to-mojo-1-0-0/3394), [lightbug_http](https://prefix.dev/channels/mojo-community/packages/lightbug_http)
- Agents and Mojo: [Modular coding assistants](https://docs.modular.com/coding-assistants/), [Mojo skills](https://mojolang.org/docs/tools/skills/), [MojoBench](https://arxiv.org/abs/2410.17736), [survey of low-resource languages](https://arxiv.org/pdf/2410.03981)
- Fine-tuning on 12 GB: [Unsloth requirements](https://docs.unsloth.ai/get-started/beginner-start-here/unsloth-requirements), [LoRA on consumer hardware](https://insiderllm.com/guides/fine-tuning-local-lora-qlora/)
- Local-first ingestion, for comparison: [EgoVault](https://github.com/milika/EgoVault), [screenpipe](https://mcpservers.org/de/servers/mediar-ai/screenpipe), [Rowboat](https://www.mintlify.com/rowboatlabs/rowboat/core-concepts/local-first)
- Judging proactive assistants: [Proactive Agent](https://arxiv.org/pdf/2410.12361), [ProAgentBench](https://arxiv.org/pdf/2602.04482), [Assistance or Disruption?](https://arxiv.org/pdf/2502.18658), [workplace field study](https://www.springerprofessional.de/doi/10.1145/2901790.2901842)
- The memory plan and its sources: [plan-one-journal-one-index.md](plan-one-journal-one-index.md)
