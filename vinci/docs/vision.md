# Vision

vinci is a knowledge partner for one person. It helps you learn, remember and act on what you know. Every fact has a date and a source, and every answer cites the evidence it used.

## Scope

- Single-person and local-first. Other people are out of scope.
- The chat is how you use vinci. The journal is the single store. Shell commands are for scripts and debugging.
- The core is capturing, remembering and recalling what you know, with provenance. The idea pipeline (Seed → Ship) is an optional layer on top.

## Input

The journal, git commits, the Markdown vaults, and ActivityWatch window titles.

## The first output: a daily brief

Built deterministically from the journal, printed by `vinci brief` and shown in the chat. It contains:

- open commitments and review findings,
- recent facts you recorded,
- time per idea, as totals only. Raw window titles stay in the journal, reachable through recall or `open`.

Every line cites its fact. A portal page for the brief comes later, if it proves useful.

## Success

Usage first, then accuracy. Capture is a means, not a measure. Each answer you use or rate counts toward the score.

A rating is a `rate` fact: a score from 1 to 5, an optional note, and refs to the cited ids of the answer it rates. It is written only after you confirm.

## Hard limits

- Every journal write needs your confirmation.
- No personal data leaves the machine, except to a model or feed you configured yourself.

## Order of work

1. Rewrite the README to lead with the knowledge partner and the chat. Move the pipeline and shell commands into their own docs.
2. Build the daily brief.
3. Record usage of each answer, so the success measure exists.
4. Add accuracy ratings.

Each step ships before the next starts.

## Where this differs from the earlier plan

The mark-3 plan (`docs/mark3-plan.md`) described the product as "a COO that proposes". That direction is now a later layer, after the brief has proved useful.
