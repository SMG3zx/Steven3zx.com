#!/bin/sh
# Proves the golden gate can fail, and that the goldens agree with the live oracle.
#   1. a wrong candidate (a wrapper that changes the first line of every output) fails the
#      cli, chat and sync golden checks
#   2. tampered copies of the goldens fail their harness: a changed status (portal), a changed
#      stdout (cli, chat, sync), and a changed journal link (cli, which exits 2: a golden that
#      does not check out)
#   3. with the C# oracle present (VINCI_ORACLE, or ~/.local/share/vinci-csharp-oracle), each
#      harness run live (--oracle) and against the goldens reports no divergence
# Run from mojo/: sh scripts/gate-selftest.sh     Exit 0 when every check behaves as expected.
set -u
root=$(cd "$(dirname "$0")/.." && pwd) || exit 2
cd "$root" || exit 2
unset VIRTUAL_ENV
cand=${CANDIDATE:-$root/build/vinci}
oracle=${VINCI_ORACLE:-$HOME/.local/share/vinci-csharp-oracle/vinci}
fixture="$root/difftest/fixtures/mid-project.jsonl"
work=$(mktemp -d) || exit 2
trap 'rm -rf "$work"' EXIT
failed=0

# expect NAME WANT COMMAND...: WANT is "fail" (non-zero exit) or "pass" (zero exit).
expect() {
	name=$1
	want=$2
	shift 2
	"$@" > "$work/$name.log" 2>&1
	code=$?
	if [ "$want" = fail ] && [ "$code" -ne 0 ]; then
		echo "PASS  $name: exit $code, the gate fails as it should"
	elif [ "$want" = pass ] && [ "$code" -eq 0 ]; then
		echo "PASS  $name: exit 0"
	else
		echo "FAIL  $name: exit $code, expected $want (log: $work/$name.log)"
		failed=$((failed + 1))
	fi
}

in_py() {
	(cd "$root/py" && "$@")
}

# 1. A wrong candidate: the same binary, with the first line of standard output changed.
cat > "$work/wrong" <<WRONG
#!/bin/sh
"$cand" "\$@" > "$work/out.\$\$" 2> "$work/err.\$\$"
code=\$?
sed '1s/^/X/' "$work/out.\$\$"
cat "$work/err.\$\$" >&2
exit \$code
WRONG
chmod +x "$work/wrong"
export VINCI_ORACLE=/nonexistent/vinci-oracle
expect cli-wrong-candidate fail in_py uv run python -m vinci_diff --candidate "$work/wrong"
expect chat-wrong-candidate fail in_py uv run python -m vinci_diff.chat_diff --candidate "$work/wrong"
expect sync-wrong-candidate fail in_py uv run python -m vinci_diff.sync_diff --candidate "$work/wrong"

# 2. Tampered goldens. Each copy changes one recorded value, and the harness must notice.
tamper() {
	# tamper DIR KIND: edits one recorded value in a copy of the goldens.
	python3 - "$1" "$2" <<'PY'
import json, pathlib, sys

root, kind = pathlib.Path(sys.argv[1]), sys.argv[2]


def edit(path, change):
    data = json.loads(path.read_text(encoding="utf-8"))
    change(data)
    path.write_text(json.dumps(data, indent=1, sort_keys=True, ensure_ascii=False) + "\n", encoding="utf-8")


if kind == "portal-status":
    edit(root / "portal" / "staging.json", lambda d: d["requests"][0].__setitem__("status", 201))
elif kind == "cli-stdout":
    edit(root / "cli" / "first-session.json", lambda d: d["outcomes"][0].__setitem__("stdout", d["outcomes"][0]["stdout"] + "X"))
elif kind == "cli-journal":
    edit(root / "cli" / "first-session.json", lambda d: d["outcomes"][1]["journal"].__setitem__("sha256", "0" * 64))
elif kind == "chat-stdout":
    edit(root / "chat" / "plain-answer.json", lambda d: d.__setitem__("stdout", d["stdout"] + "X"))
elif kind == "sync-stdout":
    edit(root / "sync" / "no_server.json", lambda d: d["outcomes"][0].__setitem__("stdout", d["outcomes"][0]["stdout"] + "X"))
else:
    raise SystemExit("unknown tamper " + kind)
PY
}
for kind in portal-status cli-stdout cli-journal chat-stdout sync-stdout; do
	mkdir -p "$work/g-$kind" && cp -R "$root/difftest/golden/." "$work/g-$kind/" || failed=$((failed + 1))
	tamper "$work/g-$kind" "$kind" || { echo "FAIL  tamper $kind did not apply"; failed=$((failed + 1)); }
done
expect portal-tampered-status fail env VINCI_GOLDEN_DIR="$work/g-portal-status" \
	sh -c "cd '$root/py' && uv run python -m vinci_diff.portal_diff --candidate '$cand'"
expect cli-tampered-stdout fail env VINCI_GOLDEN_DIR="$work/g-cli-stdout" \
	sh -c "cd '$root/py' && uv run python -m vinci_diff --candidate '$cand'"
expect cli-tampered-journal-link fail env VINCI_GOLDEN_DIR="$work/g-cli-journal" \
	sh -c "cd '$root/py' && uv run python -m vinci_diff --candidate '$cand'"
expect chat-tampered-stdout fail env VINCI_GOLDEN_DIR="$work/g-chat-stdout" \
	sh -c "cd '$root/py' && uv run python -m vinci_diff.chat_diff --candidate '$cand'"
expect sync-tampered-stdout fail env VINCI_GOLDEN_DIR="$work/g-sync-stdout" \
	sh -c "cd '$root/py' && uv run python -m vinci_diff.sync_diff --candidate '$cand'"
unset VINCI_ORACLE

# 3. Faithfulness: the goldens and the live oracle agree on the unchanged candidate.
if [ -x "$oracle" ]; then
	expect cli-golden pass in_py uv run python -m vinci_diff --candidate "$cand"
	expect cli-live pass in_py uv run python -m vinci_diff --oracle --oracle-binary "$oracle" --candidate "$cand" --generated 50 --seed 1
	expect chat-golden pass in_py uv run python -m vinci_diff.chat_diff --candidate "$cand"
	expect chat-live pass in_py uv run python -m vinci_diff.chat_diff --oracle --oracle-binary "$oracle" --candidate "$cand"
	expect portal-golden pass in_py uv run python -m vinci_diff.portal_diff --candidate "$cand"
	expect portal-live pass in_py uv run python -m vinci_diff.portal_diff --oracle --oracle-binary "$oracle" --journal "$fixture" --candidate "$cand"
	expect sync-golden pass in_py uv run python -m vinci_diff.sync_diff --candidate "$cand"
	expect sync-live pass in_py uv run python -m vinci_diff.sync_diff --oracle --oracle-binary "$oracle" --candidate "$cand"
else
	echo "SKIP  faithfulness: no C# oracle at $oracle (the goldens still run in make gate)"
fi

if [ "$failed" -eq 0 ]; then
	echo "selftest: every check behaved as expected"
	exit 0
fi
echo "selftest: $failed checks did not behave as expected"
exit 1
