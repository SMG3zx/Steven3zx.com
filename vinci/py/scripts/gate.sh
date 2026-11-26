#!/bin/sh
# The regression gate, run as `sh scripts/gate.sh` from py/. In order:
#   tests     the pytest suite (py/tests): the core, the commands, the feeds, the portal, the chat
#   cli       the journal commands against the goldens (73 scenarios, 3,613 steps)
#   chat      the chat scripts that the recorded oracle also runs (3 goldens, plus the Python-only scripts)
#   portal    the portal's requests across four journal kinds (136 requests)
#   sync      the tracker's ten cases against a fake ActivityWatch
#   personal, feed, lme   the eval thresholds of eval/gate-thresholds.json, on copies
# One line per stage, then GATE PASS or GATE FAIL (stages). Non-zero exit on any failure.
# No .NET and no C# oracle are needed: VINCI_ORACLE points at nothing, so a live oracle
# run would fail. The eval stages need ~/.vinci/eval and ~/.vinci/staging (this machine);
# a SKIP fails the gate unless GATE_ALLOW_SKIP=1.
set -u
root=$(cd "$(dirname "$0")/.." && pwd) || exit 2
cd "$root" || exit 2
unset VIRTUAL_ENV
export VINCI_ORACLE=/nonexistent/vinci-oracle
cand="${CANDIDATE:-$root/vinci}"
work=$(mktemp -d) || exit 2
trap 'rm -rf "$work"' EXIT
: > "$work/failed"

fail_stage() {
	echo "$1" >> "$work/failed"
}

# run NAME DETAIL-PATTERN COMMAND...: runs a stage, prints its line, shows the log if it failed.
run() {
	name=$1
	pattern=$2
	shift 2
	if "$@" > "$work/$name.log" 2>&1; then
		status=PASS
	else
		status=FAIL
		fail_stage "$name"
	fi
	detail=$(grep -E "$pattern" "$work/$name.log" | tail -1 | sed 's/^ *//')
	echo "STAGE $name $status $detail"
	if [ "$status" = FAIL ]; then
		tail -n 40 "$work/$name.log" | sed 's/^/    /'
	fi
}

run tests "passed|failed|error" sh -c 'uv run --group dev python -m pytest -q tests'
run cli "steps matching" sh -c "uv run python -m vinci_diff --candidate '$cand'"
run chat "scripts pass" sh -c "uv run python -m vinci_diff.chat_diff --candidate '$cand'"
run portal "requests matching" sh -c "uv run python -m vinci_diff.portal_diff --candidate '$cand'"
run sync "cases match" sh -c "uv run python -m vinci_diff.sync_diff --candidate '$cand'"

# The eval stages print their own STAGE lines.
uv run python -m vinci_eval.gate --binary "$cand" > "$work/eval.log" 2>&1
evals_status=$?
grep '^STAGE ' "$work/eval.log" > "$work/stages" || true
if [ ! -s "$work/stages" ]; then
	echo "STAGE evals FAIL no stage output (exit $evals_status)"
	fail_stage evals
	tail -n 40 "$work/eval.log" | sed 's/^/    /'
fi
# The numbers each stage measured, beside their floors, are printed before the STAGE lines.
grep -v '^STAGE ' "$work/eval.log" | sed 's/^/    /' || true
while read -r _ name status rest; do
	echo "STAGE $name $status $rest"
	if [ "$status" = SKIP ] && [ "${GATE_ALLOW_SKIP:-0}" = 1 ]; then
		echo "    GATE_ALLOW_SKIP=1: the $name skip does not fail the gate"
	elif [ "$status" != PASS ]; then
		fail_stage "$name"
	fi
done < "$work/stages"
if [ "$evals_status" -ne 0 ] && [ ! -s "$work/failed" ]; then
	# A non-zero exit that no STAGE line explains.
	fail_stage evals
fi

if [ -s "$work/failed" ]; then
	list=$(paste -sd, "$work/failed" | sed 's/,/, /g')
	echo "GATE FAIL ($list)"
	exit 1
fi
echo "GATE PASS"
exit 0
