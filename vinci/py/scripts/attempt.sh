#!/bin/sh
# Usage: scripts/attempt.sh <task-dir>
# Runs format check, build and tests for one calibration task, prints the full output,
# and appends one JSON line to <task-dir>/attempts.jsonl. Exits non-zero on failure.
#
# Build: every non-test .mojo file in the task that defines `def main(` is built.
# Tests: every test_*.mojo in the task is run with `mojo run -I <task>`. No tests = fail.
# Steps stop at the first failure, so first_error names the earliest problem.
set -u

[ $# -eq 1 ] || { echo "usage: scripts/attempt.sh <task-dir>" >&2; exit 2; }
task=$(cd "$1" 2>/dev/null && pwd) || { echo "attempt: no such task dir: $1" >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root" || exit 2
unset VIRTUAL_ENV

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
log="$work/log"
failed="$work/failed"
start=$(date +%s.%N)

step() {
	# Runs one labelled step; on failure, marks the attempt failed.
	label=$1
	shift
	echo "== $label"
	if ! "$@"; then
		echo "attempt: $label failed" >&2
		: > "$failed"
		return 1
	fi
}

check_format() {
	# mojo format rewrites in place and has no check mode, so format copies.
	mkdir -p "$work/fmt"
	cp "$task"/*.mojo "$work/fmt/" 2>/dev/null || { echo "no .mojo files in $task"; return 1; }
	uv run mojo format "$work/fmt" >/dev/null || { echo "format: mojo format could not parse the sources"; return 1; }
	for f in "$task"/*.mojo; do
		name=$(basename "$f")
		if ! diff -u "$f" "$work/fmt/$name"; then
			echo "$name is not formatted (run: uv run mojo format $name)"
			return 1
		fi
	done
	echo "format: ok"
}

build_mains() {
	found=0
	for f in "$task"/*.mojo; do
		case "$(basename "$f")" in test_*) continue ;; esac
		grep -q 'def main(' "$f" || continue
		found=1
		name=$(basename "$f" .mojo)
		uv run mojo build -I "$task" "$f" -o "$work/$name" || return 1
		echo "built $name"
	done
	[ "$found" -eq 1 ] || echo "build: no main programs to build, tests compile the sources"
	return 0
}

run_tests() {
	found=0
	for t in "$task"/test_*.mojo; do
		[ -f "$t" ] || continue
		found=1
		echo "-- $(basename "$t")"
		uv run mojo run -I "$task" "$t" || return 1
	done
	if [ "$found" -eq 0 ]; then
		echo "tests: no test_*.mojo files in $task" >&2
		return 1
	fi
	return 0
}

{
	step format check_format && step build build_mains && step test run_tests
} 2>&1 | tee "$log"

end=$(date +%s.%N)
if [ -e "$failed" ]; then pass=false; else pass=true; fi
seconds=$(awk -v a="$start" -v b="$end" 'BEGIN { printf "%.2f", b - a }')

attempts="$task/attempts.jsonl"
if [ -s "$attempts" ]; then n=$(($(wc -l < "$attempts") + 1)); else n=1; fi
at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
# first_error is the one line that says why the attempt failed, chosen in this order:
# a compiler diagnostic, an unformatted file, a test failure, then the first line of the failing step.
# Source lines that merely contain "Error" (an echoed `raise Error(...)`) do not count.
# The stack-trace hint is noise, so it is skipped.
first_error=""
if [ "$pass" = false ]; then
	filtered=$(grep -v 'stack trace was not collected' "$log" || true)
	first_error=$(printf '%s\n' "$filtered" | grep -m1 -E ':[0-9]+:[0-9]+: error:' || true)
	[ -n "$first_error" ] || first_error=$(printf '%s\n' "$filtered" | grep -m1 'not formatted' || true)
	[ -n "$first_error" ] || first_error=$(printf '%s\n' "$filtered" | grep -m1 -E 'AssertionError|Unhandled exception|error:' || true)
	# The failing step is the section after the last "== label" header.
	[ -n "$first_error" ] || first_error=$(printf '%s\n' "$filtered" | awk '/^== /{found=""; seen=1; next} seen && found=="" && NF {found=$0} END {print found}')
fi

jq -cn --arg at "$at" --argjson n "$n" --argjson pass "$pass" \
	--argjson seconds "$seconds" --arg first_error "$first_error" \
	'{at: $at, n: $n, pass: $pass, seconds: $seconds, first_error: $first_error}' >> "$attempts"

echo "attempt $n: pass=$pass seconds=$seconds (logged to $attempts)"
[ "$pass" = true ]
