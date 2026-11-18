# The method: where it comes from, and how it improves itself

## Where each part comes from

| Part of vinci | Taken from |
| --- | --- |
| A requirement needs the name of who asked and a reason; `cut` deletes it | McNeill, *The Algorithm*, steps 1 and 2 |
| Cycle time, touch time, `flow`, time per stage, and the slowest stage | McNeill, step 4: "a process can go only as quickly as its slowest step" |
| At most 3 ideas in Build | McNeill's weekly meeting on "the two or three most pressing problems"; Dalio, "you can't have everything you want" |
| `commit`, then `kept` or `missed` a week later | McNeill, ch. 8: "set an action plan and goal, and meet in a week" |
| `for` and `used` | McNeill, ch. 7 (the customer's whole experience) and ch. 9 (eat your own dog food) |
| A goal tied to a metric, checked on every measurement | Dalio, 2.5c "establish clear metrics"; Six Sigma's Verify step |
| `issue`, then `diagnose`, then `principle`, in that order | Dalio, the 5-Step Process |
| Big issues first; issues counted by stage | Dalio, 2.2e and 2.6a "identify at which step you typically fail" |
| A principle is shown again at its stage; a rule broken twice is flagged | Six Sigma's Control step |
| `predict` with a confidence, then `result`; your record against your confidence | The scientific method; Dalio's believability, applied to yourself |
| `log`, `spec`, `stats`: control limits and defects against a spec | Shewhart's control charts; Six Sigma (DMAIC) |
| `note`, `source`, `link`, `tags`; one thought per note; unlinked notes are flagged | Zettelkasten: slips, cross-references, a subject index |
| The Prepare and Incubate stages | Graham Wallas, *The Art of Thought* (1926) |
| Tend has no exit | Your brief: grown ideas are not abandoned |

Sources:

- Jon McNeill, *The Algorithm: The Hypergrowth Formula That Transformed Tesla, Lululemon, General Motors, and SpaceX*, Portfolio/Penguin, 2026.
- Ray Dalio, *Principles: Life and Work*, Simon & Schuster, 2017.
- "Six Sigma", Wikipedia, https://en.wikipedia.org/wiki/Six_Sigma
- "Zettelkasten", Wikipedia, https://en.wikipedia.org/wiki/Zettelkasten
- "Leonardo da Vinci", Wikipedia, https://en.wikipedia.org/wiki/Leonardo_da_Vinci

## How it improves itself

`vinci review` counts what is wrong and records the count as the metric `vinci.findings`. `vinci stats vinci.findings` then shows whether the system is getting healthier, using the same control chart as any other measurement.

The software does not rewrite itself. Improvement is a loop you run: a finding becomes an issue, an issue gets a root cause, a root cause becomes a principle or a change to this code. Track vinci as an idea inside vinci and log its problems there.

## Known weak points

- **The limits are guesses.** 14 days idle for an idea in Build, 30 days for a grown idea, 14 days unused for a shipped idea, 30 days for an unchecked prediction, 3 ideas in Build, a 7-day commitment: none has been tested by use. They sit together at the top of `src/Review.cs`. Change one when a logged issue says to.
- **Control limits are a guess at the noise.** 2.66 average steps is the textbook factor for single measurements, but the limit is only as good as the data behind it. Read a special cause as a prompt to look, not a verdict.
- **Six Sigma is for the steady parts.** It reduces variation, which helps a habit or a shipped thing and hurts early exploration. Apply `spec` to what you tend, not to what you are still inventing.

## What was left out on purpose

- **A database for the journal.** One file does the job. The journal is plain text. Neo4j was used for memory until mark 2, and was removed.
- **Editing and deleting facts.** Correct a fact by recording a new one.
- **A timer.** `start` and `stop` were removed: a timer you have to remember is a timer left running. Window time is counted instead.
- **Importers for calendars, health apps and so on.** "Track everything" is `vinci log`, by hand or from a script through a pipe. Automate a source only after you know you look at its numbers. Window time and commits are the two sources that earned it.
