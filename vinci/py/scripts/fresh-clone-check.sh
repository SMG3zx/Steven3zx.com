#!/bin/sh
# Fresh-clone check for the regression gate. It proves that vinci builds, installs and passes
# its golden comparison from a clean clone, with no .NET on PATH and no C# oracle.
#
#   1. a sparse, shallow clone of vinci/ from origin (only that folder, only the newest commit)
#   2. make -C vinci/py test, with VINCI_ORACLE pointing at nothing
#   3. VINCI_APP_DIR=<tmp>/app sh vinci/install.sh <tmp>/bin (a scratch install; the portal is
#      not restarted), then a smoke test from / with a temporary VINCI_HOME, VINCI_NO_TRACK=1
#      and an empty VINCI_ENV_FILE: vinci stats, vinci index rebuild, vinci recall x, vinci help
#   4. the golden CLI comparison against the installed launcher
#
# .NET is hidden from PATH: every file in /usr/bin except dotnet is linked into a scratch bin
# directory that comes first on PATH. The network is needed for uv and the test dependencies.
#
#   sh scripts/fresh-clone-check.sh                     clone origin as it is
#   sh scripts/fresh-clone-check.sh --overlay-worktree  also copy this checkout's uncommitted
#                                                       vinci/ files into the clone (what a
#                                                       commit would hold, before it exists)
#   KEEP=1 ... keeps the temporary directory.     FRESH_REMOTE=<url> overrides origin (a
#   file:// URL clones a local copy; origin is private and needs credentials).
set -eu

overlay=0
[ "${1:-}" = "--overlay-worktree" ] && overlay=1
here=$(cd "$(dirname "$0")/../.." && pwd)
top=$(git -C "$here" rev-parse --show-toplevel)
remote=${FRESH_REMOTE:-$(git -C "$top" remote get-url origin)}
# A private remote asks for a password: fail at once instead of waiting for it.
export GIT_TERMINAL_PROMPT=0
tmp=$(mktemp -d "${TMPDIR:-/tmp}/vinci-fresh.XXXXXX")
cleanup() {
	if [ "${KEEP:-0}" = 1 ]; then
		echo "kept $tmp"
	else
		chmod -R u+w "$tmp" 2>/dev/null || true
		rm -rf "$tmp"
	fi
}
trap cleanup EXIT
step() {
	echo ""
	echo "== $*"
}

step "clone: sparse and shallow, vinci/ only, from $remote"
if ! git clone --quiet --depth 1 --filter=blob:none --sparse "$remote" "$tmp/repo"; then
	echo "FAIL: cannot clone $remote (a private remote needs credentials; set FRESH_REMOTE=file://<repo> to test a local copy)" >&2
	exit 1
fi
git -C "$tmp/repo" sparse-checkout set vinci
if [ "$overlay" = 1 ]; then
	echo "overlay: copying this checkout's uncommitted vinci/ files"
	(cd "$here" && git ls-files -co --exclude-standard) | while IFS= read -r f; do
		mkdir -p "$tmp/repo/vinci/$(dirname "$f")"
		cp "$here/$f" "$tmp/repo/vinci/$f"
	done
fi
clone="$tmp/repo/vinci"
echo "clone at $(git -C "$tmp/repo" rev-parse --short HEAD)"

# PATH without dotnet: /usr/bin's entries, minus dotnet, in a scratch folder first on PATH.
nodotnet="$tmp/nodotnet"
mkdir "$nodotnet"
for f in /usr/bin/* /usr/bin/.[!.]*; do
	[ -e "$f" ] || [ -L "$f" ] || continue
	[ "$(basename "$f")" = dotnet ] && continue
	ln -s "$f" "$nodotnet/$(basename "$f")"
done
rest=$(printf '%s' "$PATH" | tr ':' '\n' | grep -v '^/usr/bin$' | paste -sd: - || true)
export PATH="$nodotnet${rest:+:$rest}"
if command -v dotnet >/dev/null 2>&1; then
	echo "FAIL: dotnet is still on PATH" >&2
	exit 1
fi
echo "dotnet: not on PATH ($(command -v uv))"
export VINCI_ORACLE=/nonexistent/vinci-oracle
unset VIRTUAL_ENV

step "make test in the clone (no oracle, no .NET)"
make -C "$clone/py" test > "$tmp/check.log" 2>&1
tail -n 3 "$tmp/check.log"

step "scratch install (VINCI_APP_DIR=$tmp/app, the portal is not restarted)"
VINCI_APP_DIR="$tmp/app" sh "$clone/install.sh" "$tmp/bin" > "$tmp/install.log" 2>&1
tail -n 2 "$tmp/install.log"

step "smoke test from / (temporary VINCI_HOME, VINCI_NO_TRACK=1, empty VINCI_ENV_FILE)"
home="$tmp/vinci-home"
mkdir "$home"
: > "$tmp/empty.env"
smoke() {
	(cd / && env -i PATH="$PATH" HOME="$tmp" LANG=C.UTF-8 VINCI_HOME="$home" \
		VINCI_NO_TRACK=1 VINCI_ENV_FILE="$tmp/empty.env" VINCI_ORACLE="$VINCI_ORACLE" \
		"$tmp/bin/vinci" "$@")
}
smoke_failed=0
# A new home has no index yet, so the index is built first.
for args in "stats" "index rebuild" "recall x" "help"; do
	# shellcheck disable=SC2086
	if smoke $args > "$tmp/smoke.out" 2>&1; then
		echo "vinci $args: exit 0, $(wc -l < "$tmp/smoke.out") lines"
	else
		echo "vinci $args: FAILED"
		sed 's/^/    /' "$tmp/smoke.out" | tail -n 8
		smoke_failed=1
	fi
done

step "golden CLI comparison against the installed launcher"
golden_ok=0
(cd "$clone/py" && uv run python -m vinci_diff --candidate "$tmp/app/vinci") > "$tmp/golden.log" 2>&1 && golden_ok=1
tail -n 6 "$tmp/golden.log"

echo ""
if [ "$smoke_failed" = 0 ] && [ "$golden_ok" = 1 ]; then
	echo "FRESH CLONE PASS"
	exit 0
fi
echo "FRESH CLONE FAIL (see the lines above; logs in $tmp)"
KEEP=1
exit 1
