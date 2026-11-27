# Glossary

Vocabulary for vinci. Definitions only; decisions live in `docs/vision.md` and `docs/`.

**Knowledge partner.** What vinci is: it helps one person learn, remember and act on what they know, with every fact dated and sourced.

**Journal.** The single append-only store of facts. Nothing is edited or deleted; a correction is a new fact.

**Fact.** One line in the journal: a kind, a time, and optional fields such as text, refs or a value.

**Chat.** The way the person uses vinci: `vinci` with no command. The chat model acts through tools.

**Journal word.** A command that writes or reads the journal, such as `idea`, `note` or `ship`. The person runs it as `vinci journal <word>`, and the chat runs it through its `journal` tool.

**Chat tool.** One of the chat model's tools: `journal`, `recall`, `open`, `remember`, `read`.

**Recall.** Search over the index for lines about a question. Each returned line carries an id in brackets.

**Citation.** An id in brackets in an answer, pointing to a fact that recall or `open` returned.

**Idea.** A thing you are working on, with an id such as `i1`.

**Stage.** Where an idea is in the pipeline: Seed, Prepare, Incubate, Build, or Tend.

**Home.** The folder where an idea lives. Editor windows and commits there count toward the idea.

**Watch.** A text matched against window titles and app names. Time in matching windows counts toward an idea. A `use` watch records that you used what you shipped.

**Work.** A fact recording time or a commit counted toward an idea.

**Brief.** The daily summary of open commitments, review findings, recent facts and time per idea, with every line cited.

**Rating.** A `rate` fact: a score from 1 to 5 on an answer, with optional note and refs to the answer's cited ids.

**Feed.** A source read into the index: git, the vaults, the journal, or ActivityWatch.
