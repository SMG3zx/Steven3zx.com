#!/bin/sh
# Usage: diff.sh
# Builds ideas.mojo into this task's build/ folder, then diffs its output against
# the C# oracle on the real journal and on fixture.jsonl. Exits non-zero on any difference.
# The real journal is copied to /tmp/c1home first, so the source is never touched.
# `vinci ideas` may append events to its copy; the Mojo program reads that same
# post-run copy, so both sides fold the same bytes.
set -eu

dir=$(cd "$(dirname "$0")" && pwd)
mojo_root=$(cd "$dir/../.." && pwd)
real=${REAL_JOURNAL:-$HOME/.vinci/journal.jsonl}
home=/tmp/c1home
mkdir -p "$home" "$dir/build"

unset VIRTUAL_ENV
(cd "$mojo_root" && uv run mojo build -I "$dir" "$dir/ideas.mojo" -o "$dir/build/ideas")

cp "$real" "$home/real.jsonl"

status=0
for journal in "$home/real.jsonl" "$dir/fixture.jsonl"; do
	name=$(basename "$journal")
	"$dir/oracle.sh" "$journal" > "$dir/build/oracle-$name.txt"
	"$dir/build/ideas" "$home/journal.jsonl" > "$dir/build/mojo-$name.txt"
	if diff -u "$dir/build/oracle-$name.txt" "$dir/build/mojo-$name.txt"; then
		echo "diff: $name matches the oracle ($(wc -l < "$dir/build/mojo-$name.txt") lines)"
	else
		echo "diff: $name DIFFERS from the oracle" >&2
		status=1
	fi
done
exit $status
