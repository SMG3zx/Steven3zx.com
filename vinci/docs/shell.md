# Using vinci from a shell

The chat is the main way to use vinci. This page covers the shell, for scripts and debugging. Every journal command runs as `vinci journal <word>`, and `vinci help` lists the top-level commands.

## Piping into vinci

Any quoted text can be `-`, which reads it from standard input:

```sh
git log -1 --format=%s | vinci journal ship i1 -
wl-paste | vinci journal note - s1:p.42 +flight
```

A command that creates something prints its id first, and `vinci path` prints the journal file:

```sh
id=$(vinci journal idea "Canal lock" | cut -d' ' -f1)
jq -r 'select(.kind=="ship") | "\(.at[:10]) \(.text)"' "$(vinci path)"
```

Errors go to standard error and the exit code is 1.


## Top-level commands

`vinci`, `vinci chat`, `vinci brief`, `vinci serve`, `vinci index`, `vinci feed`, `vinci backup`, `vinci verify`, `vinci path` and `vinci help`.
