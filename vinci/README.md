# vinci

A command-line tool that takes an idea from first thought to shipped work and keeps it alive afterwards. It also records what you know and where you learned it, what you predicted and whether you were right, how long you spend on each idea, and anything else about your life you choose to measure.

It is written in C# on .NET 10 and has no dependencies. All data is one file.

## Install

```sh
./install.sh        # runs the tests, builds, and links ~/.local/bin/vinci
vinci               # the pipeline and the one thing to do next
vinci help          # every command
```

## The one idea underneath

Every fact is written once, with the time, to an append-only journal (`~/.vinci/journal.jsonl`). Nothing is edited or deleted. Everything vinci shows you is computed from that file each time it runs.

That one decision gives you several things without building them separately:

- **Provenance.** Every fact has a date, and a note points at the source it came from.
- **Time tracking.** Starting and stopping a timer are just two more facts.
- **Life tracking.** A measurement is a fact with a number in it.
- **Self-measurement.** The system's own defects are facts too, so it can chart them.
- **A tool that fits in a pipe.** The journal is plain JSON lines, so `jq` and `grep` work on it.

## The pipeline

```
Seed ──► Prepare ──► Incubate ──► Build ──► Tend
```

| Move | The gate | Why |
| --- | --- | --- |
| Seed → Prepare | Who it is for, and a goal tied to a metric | A goal without a number cannot be checked |
| Prepare → Incubate | A note, a questioned requirement, a prediction | Gather what is known, delete what is not needed, say what you expect |
| Incubate → Build | The wait is over, and fewer than 3 ideas are in Build | Rest is part of the work; too many open builds and none ships |
| Build → Tend | You ship | |
| Tend | No exit | Ship again, use it, tend it, or retire it with a reason |

`vinci advance <idea>` moves an idea one stage and refuses if the gate is not met.

`vinci` with no command prints the pipeline:

```
Seed       i2   Bridge  (4d0h here)
Prepare    -
Incubate   -
Build 1/3  i1   Flying machine  (4d0h here, touch 6h10m; goal not met, distance 60 needs ≥ 100)
Tend       -
Slowest stage: Build, 4d0h on average. Speed that one up first.
Next: build i1: Flying machine
      vinci start i1
```

`Next` is the worst thing `review` would find. If there is nothing to fix, it is the idea in Build that has waited longest.

## A first session

```sh
vinci idea "Flying machine"
vinci for i1 "One pilot, launching from a hill"
vinci goal i1 "Carry one person 100 m" distance at-least 100
vinci advance i1                                   # Seed -> Prepare

vinci source "Leonardo da Vinci, Codex on the Flight of Birds, 1505"
vinci note "A bird steers by shifting its centre of gravity" s1:f.7v i1 +flight
vinci req i1 "Lifts 80 kg" "Leonardo" "A person has to be on it"
vinci predict "A 10 m wing lifts 80 kg" 70 i1
vinci advance i1 3                                 # incubate for 3 days

vinci advance i1                                   # 3 days later: -> Build
vinci commit "Build the wing frame" i1             # this week's one thing
vinci start i1                                     # ... work ...
vinci stop
vinci ship i1 "Glider model"                       # -> Tend

vinci used i1 "Flew 20 m off the hill"
vinci log distance 20                              # i1 goal not met: needs ≥ 100
vinci result h1 wrong "Lifted 35 kg"
vinci note "Lift did not scale with span alone" h1

vinci issue "Wing tore on the first test" i1 big
vinci diagnose x1 "Never load-tested the cloth"
vinci principle "Test each material alone before building with it" x1

vinci review                                       # weekly
vinci kept                                         # or: vinci missed "why"
```

## In a pipe

Any quoted text can be `-`, which reads it from standard input:

```sh
git log -1 --format=%s | vinci ship i1 -
wl-paste | vinci note - s1:p.42 +flight
```

A command that creates something prints its id first, and `vinci path` prints the journal file:

```sh
id=$(vinci idea "Canal lock" | cut -d' ' -f1)
jq -r 'select(.kind=="ship") | "\(.at[:10]) \(.text)"' "$(vinci path)"
```

Errors go to standard error and the exit code is 1.

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
| `log`, `spec`, `stats`: control limits, defects, sigma level | Six Sigma (DMAIC); Shewhart's control charts |
| `note`, `source`, `link`, `tags`; one thought per note; unlinked notes are flagged | Zettelkasten: slips, cross-references, a subject index |
| The Prepare and Incubate stages | Graham Wallas, *The Art of Thought* (1926) |
| Tend has no exit | Your brief: grown ideas are not abandoned |

Sources:

- Jon McNeill, *The Algorithm: The Hypergrowth Formula That Transformed Tesla, Lululemon, General Motors, and SpaceX*, Portfolio/Penguin, 2026.
- Ray Dalio, *Principles: Life and Work*, Simon & Schuster, 2017.
- "Six Sigma", Wikipedia, https://en.wikipedia.org/wiki/Six_Sigma
- "Zettelkasten", Wikipedia, https://en.wikipedia.org/wiki/Zettelkasten
- "Leonardo da Vinci", Wikipedia, https://en.wikipedia.org/wiki/Leonardo_da_Vinci
- Peter J. Acklam's algorithm for the inverse normal distribution, used for the sigma level.

## How it improves itself

`vinci review` counts what is wrong and records the count as the metric `vinci.findings`. `vinci stats vinci.findings` then shows whether the system is getting healthier, using the same control chart as any other measurement.

The software does not rewrite itself. Improvement is a loop you run: a finding becomes an issue, an issue gets a root cause, a root cause becomes a principle or a change to this code. Track vinci as an idea inside vinci and log its problems there.

## Known weak points

- **The limits are guesses.** 14 days idle, 30 days for a grown idea, 3 ideas in Build, a 7-day commitment: none has been tested by use. They sit together at the top of `src/Review.cs`. Change one when a logged issue says to.
- **The sigma level uses the 1.5 shift**, which the Six Sigma article itself reports as criticised for being arbitrary. vinci gives no sigma level below 30 measurements. Trust the control limits over the sigma level.
- **Six Sigma is for the steady parts.** It reduces variation, which helps a habit or a shipped thing and hurts early exploration. Apply `spec` to what you tend, not to what you are still inventing.

## What was left out on purpose

- **A database, a server, a web page.** One file and a command line do the job.
- **Editing and deleting facts.** Correct a fact by recording a new one.
- **Importers for calendars, health apps and so on.** "Track everything" is `vinci log`, by hand or from a script through a pipe. Automate a source only after you know you look at its numbers.

## Privacy

The journal holds personal data and lives outside this repository. Set `VINCI_HOME` to move it. Back it up yourself; it is plain text and works well in a private git repository.

## Development

```sh
dotnet test tests
```

| File | Holds |
| --- | --- |
| `src/Journal.cs` | The event record and the append-only file |
| `src/State.cs` | Rebuilding the current state from events |
| `src/App.cs` | The commands and the stage gates |
| `src/Review.cs` | The findings, the limits, and what to do next |
| `src/Stats.cs` | Control limits and sigma level |
