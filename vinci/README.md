# vinci

vinci is a general-purpose knowledge partner. It helps you learn, remember and act on what you know. Every fact it keeps has a date and a source, and every answer points to the evidence it used. It also records your predictions and how they turned out, and it helps turn ideas into shipped work when you want that.

Written in Mojo, with small Python edges for the model, the network and the HTTP server. The journal commands need nothing else and keep all their data in one file. There is no database: the search index is a SQLite file rebuilt from the journal. Chat needs a llama-server model (see [docs/chat.md](docs/chat.md)).

## Install

```sh
./install.sh        # runs the checks, builds, and installs to ~/.local/share/vinci,
                    # with ~/.local/bin/vinci linked to it. Needs uv.
vinci               # opens the chat (the pipeline is `vinci pipeline`)
vinci help          # every command
```

The installed command runs from any directory. It does not need uv or the checkout.

## The one idea underneath

Every fact is written once, with the time, to an append-only journal (`~/.vinci/journal.jsonl`). Nothing is edited or deleted. Everything vinci shows you is computed from that file each time it runs. Correct a fact by recording a new one.

That gives you, without separate features:

- **Provenance.** Every fact has a date, and a note points at the source it came from.
- **Time tracking.** Time in an idea's windows and each commit in its folder are facts too, written for you before every command. `vinci sync` prints what was just written. ActivityWatch's window titles are also a feed (`vinci feed run activity`, off until you turn it on in `feeds.toml`).
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

`vinci advance <idea>` moves an idea one stage. If the gate is not met it says what is missing, and still moves: the gates are advice, and the choice is yours.

`vinci pipeline` prints the pipeline (`vinci` alone opens the chat):

```
Seed       i2   Bridge  (4d0h here)
Prepare    -
Incubate   -
Build 1/3  i1   Flying machine  (4d0h here, touch 6h10m; goal not met, distance 60 needs ≥ 100)
Tend       -
Slowest stage: Build, 4d0h on average. Speed that one up first.
Next: build i1: Flying machine
      vinci ship i1 "what shipped"
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
vinci home i1 ~/code/glider                        # once: where the idea lives
                                                   # ... work; time and commits are counted for you ...
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

## Using it from a shell

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

## Where to read more

| Topic | Document |
| --- | --- |
| Talking to vinci in the chat, and its model setup | [docs/chat.md](docs/chat.md) |
| Time and commits counted from your windows and folders; ActivityWatch setup | [docs/time-tracking.md](docs/time-tracking.md) |
| The portal of saved, project and local links (`vinci serve`) | [docs/portal.md](docs/portal.md) |
| Memory: the journal, the index, recall, and what mark 2 removed (`vinci memory`) | [docs/memory.md](docs/memory.md) |
| Feeds: git, the Markdown vaults and the journal read into the index (`vinci feed`), and their contract | [docs/feeds.md](docs/feeds.md) |
| Where each part of the method comes from, how vinci improves itself, known weak points, what was left out on purpose | [docs/method.md](docs/method.md) |
| Code map and tests | [docs/development.md](docs/development.md) |

## Privacy

The journal holds personal data and lives outside this repository. Set `VINCI_HOME` to move it. Back it up yourself; it is plain text and works well in a private git repository.
