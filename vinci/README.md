# vinci

vinci is a knowledge partner. It helps you learn, remember and act on what you know. Every fact it keeps has a date and a source, and every answer points to the evidence it used.

Talk to it in the chat. The chat model keeps your ideas, notes, time and goals in one journal, and it looks things up from there. The journal is a plain file on your machine, and nothing leaves it except to the model and feeds you configure. For the full statement of what vinci is for, read [docs/vision.md](docs/vision.md).

## Install

```sh
./install.sh        # runs the checks, builds, and installs to ~/.local/share/vinci,
                    # with ~/.local/bin/vinci linked to it. Needs uv.
vinci               # opens the chat
vinci help          # the top-level commands
```

The installed command runs from any directory. It does not need uv or the checkout. Chat needs a llama-server model; see [docs/chat.md](docs/chat.md).

## What it does

- **Remembers.** Each fact is written once, with its time, to an append-only journal. Corrections are new facts, so nothing is lost.
- **Recalls with sources.** A question searches the journal, your git commits and your Markdown vaults. Each answer cites the lines it used, and you can open any of them.
- **Counts your time.** With ActivityWatch running, time in an idea's windows and the commits in its folder are recorded for you.
- **Tracks what you predict and measure.** Predictions, results and measurements are facts too, so you can see how your expectations compare with outcomes.
- **Helps you ship ideas (optional).** An idea moves through five stages with a gate at each move. See [docs/pipeline.md](docs/pipeline.md).

## An example chat

```text
you> Make an idea called Flying machine for one pilot.
  · journal {"args":["idea","Flying machine"]}
Created the idea "Flying machine" for one pilot.

you> What did I say about gliders?
  · recall {"query":"what did I say about gliders"}
You noted that a bird steers by shifting its centre of gravity [s1/f.7v].
```

The chat shows each tool it runs, so you can see where an answer came from.

## Where to read more

| Topic | Document |
| --- | --- |
| What vinci is for, and what it is not | [docs/vision.md](docs/vision.md) |
| The chat, its tools and its model setup | [docs/chat.md](docs/chat.md) |
| The idea pipeline: stages, gates, and a worked session | [docs/pipeline.md](docs/pipeline.md) |
| The shell, for scripts and debugging | [docs/shell.md](docs/shell.md) |
| Time and commits counted from your windows and folders; ActivityWatch setup | [docs/time-tracking.md](docs/time-tracking.md) |
| The portal of saved, project and local links (`vinci serve`) | [docs/portal.md](docs/portal.md) |
| Memory: the journal, the index, and recall | [docs/memory.md](docs/memory.md) |
| Feeds: git, the vaults, and the journal read into the index | [docs/feeds.md](docs/feeds.md) |
| Where the method comes from, and its known weak points | [docs/method.md](docs/method.md) |
| Code map and tests | [docs/development.md](docs/development.md) |
| Vocabulary | [GLOSSARY.md](GLOSSARY.md) |

## Privacy

The journal holds personal data and lives outside this repository. Set `VINCI_HOME` to move it. Back it up with `vinci backup`, which copies it with a sha256 manifest. `vinci verify` checks it without writing (see [docs/development.md](docs/development.md#backup-and-verify)). The journal is plain text and works well in a private git repository too.
