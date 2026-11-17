# M3 results: one journal, one index (2026-11-17)

M3 of [the mark 2 plan](mark2-plan.md) carried out [the one-journal plan](plan-one-journal-one-index.md), phases 1 to 3, in Mojo. Here is what was built, what it measured, and what is still open.

## What was built

| Step | Commit | What |
|---|---|---|
| 3.1 | 1d848de | Memory kinds in the journal, and `vinci memory import` from Neo4j. The real journal was imported after a backup, so the journal is now the store of record. |
| 3.2a | 002762a | The index: SQLite + FTS5, rebuilt from the journal in 0.3 s, versioned by hash. Lexical recall. |
| 3.2 | 064d2a5 | An embedding cache keyed by model label, and the LongMemEval embedding job. |
| 3.2b | 3e282a0 | Vectors (a SIMD scan), RRF fusion, abstention, the corrective query and date boosts, ported from today's recall. |
| 3.3a | 2dc352e | Handles, `vinci open <id> [lines=] [grep=]`, and `vinci recall --budget N --whole W`, which packs whole records and one-line pointers. |
| 3.3b | b3e45f3 | The budget sweep: Qwen 27B answers from the packed context, and a judge scores the answers. |

The differential test against the C# binary still passes: 683 of 683 steps match.

## Recall (from 3.2b and 3.3a)

- **Personal v2** (60 questions, 48 answerable):
  - Lexical + vector finds 36/48 answers and gets 49/60 abstentions right, compared with 35/48 and 48/60 for the M1 baseline.
- **LongMemEval** (100 fixed questions):
  - any@10 is 98.9% and all@10 is 94.4%. The reference retriever's all@10 is 88.9%.
  - Vector alone reaches all@10 98.9% with fewer tokens, so the fusion weights are worth tuning later.

## Answer accuracy at each budget (3.3b)

Results file: `~/.vinci/eval/results/budget-sweep-2026-11-17.jsonl`, 1,060 answers, 0 errors. Qwen 27B (`swift-qwen3.8-27b`) answers with thinking off. The same model judges with LongMemEval-style prompts. Those prompts are reconstructed rather than copied verbatim, and a model judging its own answers is biased, so compare rows with each other, not with published scores.

**LongMemEval**

| config | accuracy | mean context tokens | whole / pointers / omitted |
|---|---|---|---|
| baseline (top 10 whole) | 69/100 | 2,461 | 10 / 0 / 0 |
| b500w2 | 32/100 | 473 | 1.7 / 6.5 / 1.9 |
| b1000w2 | 36/100 | 678 | 1.9 / 7.5 / 0.6 |
| b1000w10 | 58/100 | 955 | 5.6 / 1.7 / 2.7 |
| b2000w10 | 66/100 | 1,747 | 8.5 / 1.1 / 0.5 |
| b3000w10 | 67/100 | 2,243 | 9.7 / 0.3 / 0.1 |
| b5000w10 | 69/100 | 2,445 | 10.0 / 0.0 / 0.0 |

**Personal v2** (the lexical column means the answer contains every expected string, counted over the 48 answerable questions)

| config | judge | lexical | mean context tokens |
|---|---|---|---|
| baseline | 33/60 | 29/48 | 227 |
| b100w2 | 26/60 | 22/48 | 88 |
| b150w2 | 29/60 | 24/48 | 136 |
| b250w2 | 32/60 | 29/48 | 212 |
| b250w10 | 33/60 | 29/48 | 211 |
| b500w10 | 33/60 | 29/48 | 227 |

### What it says

1. **The done criterion is met, but only just.** Two configurations match baseline accuracy with fewer tokens:
   - Personal b250w10 gets 33/60 with 211 tokens instead of 227, about 7% fewer.
   - LongMemEval b5000w10 gets 69/100 with 2,445 tokens instead of 2,461, under 1% fewer.

   b3000w10 gives up 2 questions to save 9%, and b2000w10 gives up 3 to save 29%. Budgets save real tokens only at some cost in accuracy.
2. **Pointers cost answers when the model cannot open them.**
   - On LongMemEval, two whole records plus pointers lose about half the accuracy: 32 to 36 correct, against 69 for the baseline.
   - The sweep gives the model no `open` tool, so a pointer is only a hint it cannot follow.

   The pointer form is built for the M4 chat, where the model can call `open`. It has to be measured again there, with the tool, before it can be judged. Until then, the default for a model without tools should be `--whole 10` (fill the budget with whole records).
3. **Where answers are lost.** Multi-session and temporal-reasoning questions lose the most as the budget shrinks, since they need several records at once. Knowledge-update questions hold up even at b1000w10 (14/15).
4. **single-session-preference is 0/5 in every configuration**, the baseline included. That is a problem with the answer prompt or the judge, not with recall: recall finds these records. It is for M4, where the chat prompt is written.
5. **The judge errs.** On personal j-01 the answer contains the expected path, but the judge says no in every configuration. Personal accuracy is low in absolute terms (55%). The lexical column is the steadier signal there.

## Open, carried to M4

- Measure pointers with an `open` tool in the chat loop. Budgeted accuracy with tools is the real phase 3 question.
- The summarize step and the write confirmation, moved from M3 to M4.
- The single-session-preference prompt (0/5).
- Tune the fusion weights, since vector alone beat fusion on all@10.
- `vinci open i1` prints all of an id's facts, each up to its cap, but the total has no cap. The chat tool needs a cap on the whole output.
