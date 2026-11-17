#!/bin/sh
# Usage: oracle.sh <journal>
# Runs the C# `vinci ideas` on a fresh copy of the journal and strips the age and
# flow columns, so the output can be compared with ideas.mojo.
# The copy lives in /tmp/c1home. The source journal is never written.
set -eu

[ $# -eq 1 ] || { echo "usage: oracle.sh <journal>" >&2; exit 2; }
src=$1
home=/tmp/c1home
mkdir -p "$home"
copy="$home/journal.jsonl"
cp "$src" "$copy"

out=$(VINCI_HOME="$home" vinci ideas)

# `vinci ideas` is not read-only: window and git tracking run first and may append
# spent, sync and work events. The C# output is a fold over the journal as it is
# after that run, so the copy is left as it is and diff.sh gives the Mojo program
# this same post-run copy.
before=$(wc -l < "$src")
after=$(wc -l < "$copy")
if [ "$before" != "$after" ]; then
	echo "oracle: vinci appended $((after - before)) events to the copy; the Mojo side reads the post-run copy" >&2
fi

# Line shape: "{id,-4} {stage,-8} touch {span,-7} age {age,-7} flow {flow,-6} {title}".
# Cut at the first " age " (the span has no spaces, so it comes first), then drop the
# age and flow tokens and their padding. The title is kept exactly.
printf '%s\n' "$out" | awk '
{
	line = $0
	p = index(line, " age ")
	if (p == 0) { print line; next }
	head = substr(line, 1, p - 1)
	rest = substr(line, p + 5)
	q = index(rest, " flow ")
	rest = substr(rest, q + 6)
	n = index(rest, " ")
	tok = n - 1
	pad = (tok < 6 ? 6 - tok : 0) + 1
	print head " " substr(rest, tok + pad + 1)
}'
