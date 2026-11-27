# The idea pipeline

An optional layer on top of the knowledge partner. Ideas move through five stages, and each move has a gate. Use it when you are turning an idea into shipped work; the rest of vinci works without it.

In the chat, say what you want ("move the flying machine to Build"). From a shell, the same commands run as `vinci journal <word>`.

## Stages and gates

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

`vinci journal advance <idea>` moves an idea one stage. If the gate is not met it says what is missing, and still moves: the gates are advice, and the choice is yours.

`vinci journal pipeline` prints the pipeline (`vinci` alone opens the chat):

```
Seed       i2   Bridge  (4d0h here)
Prepare    -
Incubate   -
Build 1/3  i1   Flying machine  (4d0h here, touch 6h10m; goal not met, distance 60 needs ≥ 100)
Tend       -
Slowest stage: Build, 4d0h on average. Speed that one up first.
Next: build i1: Flying machine
      vinci journal ship i1 "what shipped"
```

`Next` is the worst thing `review` would find. If there is nothing to fix, it is the idea in Build that has waited longest.


## A worked session (shell form)

```sh
vinci journal idea "Flying machine"
vinci journal for i1 "One pilot, launching from a hill"
vinci journal goal i1 "Carry one person 100 m" distance at-least 100
vinci journal advance i1                                   # Seed -> Prepare

vinci journal source "Leonardo da Vinci, Codex on the Flight of Birds, 1505"
vinci journal note "A bird steers by shifting its centre of gravity" s1:f.7v i1 +flight
vinci journal req i1 "Lifts 80 kg" "Leonardo" "A person has to be on it"
vinci journal predict "A 10 m wing lifts 80 kg" 70 i1
vinci journal advance i1 3                                 # incubate for 3 days

vinci journal advance i1                                   # 3 days later: -> Build
vinci journal commit "Build the wing frame" i1             # this week's one thing
vinci journal home i1 ~/code/glider                        # once: where the idea lives
                                                   # ... work; time and commits are counted for you ...
vinci journal ship i1 "Glider model"                       # -> Tend

vinci journal used i1 "Flew 20 m off the hill"
vinci journal log distance 20                              # i1 goal not met: needs ≥ 100
vinci journal result h1 wrong "Lifted 35 kg"
vinci journal note "Lift did not scale with span alone" h1

vinci journal issue "Wing tore on the first test" i1 big
vinci journal diagnose x1 "Never load-tested the cloth"
vinci journal principle "Test each material alone before building with it" x1

vinci journal review                                       # weekly
vinci journal kept                                         # or: vinci journal missed "why"
```


## Time and commits

Time in an idea's windows and each commit in its folder are counted for you before every command. See [time-tracking.md](time-tracking.md).
