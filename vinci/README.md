# vinci

A command-line tool that takes an idea from first thought to shipped work and keeps it alive afterwards. It also records what you know and where you learned it, how long you spend on each idea, and anything else about your life you choose to measure.

It is written in C# on .NET 10 and has no dependencies. All data is one file.

## The one idea underneath

Every fact is written once, with the time, to an append-only journal (`~/.vinci/journal.jsonl`). Nothing is edited or deleted. Everything vinci shows you is computed from that file each time it runs.

That one decision gives you four of the things you asked for without building them separately:

- **Provenance.** Every fact has a date, and a note points at the source it came from.
- **Time tracking.** Starting and stopping a timer are just two more facts.
- **Life tracking.** A measurement is a fact with a number in it.
- **Self-measurement.** The system's own defects are facts too, so it can chart them.

## Where each part comes from

| Part of vinci | Taken from | What it does here |
| --- | --- | --- |
| A requirement needs the name of the person who asked and a reason; `cut` deletes it | McNeill, *The Algorithm*, steps 1 and 2 | You cannot leave Prepare without at least one requirement that survived questioning |
| Cycle time, touch time, and `flow` | McNeill, *The Algorithm*, step 4 | `flow` is touch time divided by the idea's age. The rest is waiting, and that is where to improve |
| `review`, run weekly | McNeill's weekly cadence; Dalio's "look at the machine from the higher level" | Lists what is neglected, unlinked, undiagnosed or out of control |
| Goal before anything else; `issue`, then `diagnose`, then `principle`, in that order | Dalio, *Principles*, the 5-Step Process | You cannot write a principle for an issue that has no root cause |
| `log`, `spec`, `stats`: control limits, defects per million, sigma level | Six Sigma (DMAIC); Shewhart's control charts | Tells ordinary variation from a special cause, for any number you track |
| `note`, `source`, `link`; one thought per note; notes that link to nothing are flagged | Luhmann's Zettelkasten | Knowledge with credit attached |
| The Prepare and Incubate stages | Graham Wallas, *The Art of Thought* (1926) | Incubation is a stage with an end date. vinci refuses to advance early |
| Tend has no exit | Your brief: grown ideas are not abandoned | A grown idea is shipped again, tended, or retired on purpose with a reason |

Sources:

- Jon McNeill, *The Algorithm: The Hypergrowth Formula That Transformed Tesla, Lululemon, General Motors, and SpaceX*, Portfolio/Penguin, 2026.
- Ray Dalio, *Principles: Life and Work*, Simon & Schuster, 2017.
- "Six Sigma", Wikipedia, https://en.wikipedia.org/wiki/Six_Sigma
- "Zettelkasten", Wikipedia, https://en.wikipedia.org/wiki/Zettelkasten
- Peter J. Acklam's algorithm for the inverse normal distribution, used for the sigma level.

## The life of an idea

```
Seed ──► Prepare ──► Incubate ──► Build ──► Tend
     goal      notes and     time has     ship      (ship again, tend, or retire)
               requirements  passed
```

`vinci advance <idea>` moves an idea forward one stage and refuses if the gate is not met. Shipping is what moves an idea from Build to Tend, and an idea in Tend can ship as often as you like.

## Install

```sh
cd vinci
dotnet build src -c Release
alias vinci="dotnet $PWD/src/bin/Release/net10.0/vinci.dll"   # add to your shell config
```

## A first session

```sh
vinci idea "Flying machine"
vinci goal i1 "Carry one person 100 m"
vinci advance i1                                   # Seed -> Prepare

vinci source "Leonardo da Vinci, Codex on the Flight of Birds, 1505"
vinci note "A bird steers by shifting its centre of gravity" s1 i1
vinci req i1 "Lifts 80 kg" "Leonardo" "A person has to be on it"
vinci advance i1 3                                 # incubate for 3 days

vinci advance i1                                   # 3 days later: -> Build
vinci start i1                                     # ... work ...
vinci stop
vinci ship i1 "Glider model"                       # -> Tend

vinci log sleep 7.5                                # measure anything
vinci spec sleep 7 9
vinci stats sleep

vinci issue "Wing tore on the first test" i1
vinci diagnose x1 "Never load-tested the cloth"
vinci principle "Test each material alone before building with it" x1

vinci review                                       # weekly
```

`vinci help` lists every command.

## How it improves itself

`vinci review` counts what is wrong and records the count as the metric `vinci.findings`. `vinci stats vinci.findings` then shows whether the system is getting healthier, using the same control chart as any other measurement.

The software does not rewrite itself. Improvement is a loop you run: a finding becomes an issue, an issue gets a root cause, a root cause becomes a principle or a change to this code. Track vinci as an idea inside vinci and log its problems there.

## What was left out on purpose

Each of these was a candidate requirement and was cut. Add one back when a logged issue shows you need it.

- **A database, a server, a web page.** One file and a command line do the job.
- **Editing and deleting facts.** Correct a fact by recording a new one.
- **Importers for calendars, health apps and so on.** "Track everything" starts as `vinci log`. Automating comes last, after you know which numbers you look at.
- **Believability-weighted decisions** (Dalio). It needs more than one person.
- **Note types** (fleeting, literature, permanent). A note with a source is a literature note; a note without one is your own thought. No label needed.

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
| `src/Review.cs` | The findings |
| `src/Stats.cs` | Control limits and sigma level |
