#!/bin/sh
# Fails if `mojo format` would change any file under src/ or tests/.
# Formats a copy, because `mojo format` has no check mode and rewrites in place.
set -e
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp -R src tests "$tmp/"
uv run mojo format "$tmp/src" "$tmp/tests" >/dev/null
if ! diff -ru src "$tmp/src" >/dev/null || ! diff -ru tests "$tmp/tests" >/dev/null; then
	echo "fmt-check: not formatted. Run 'make fmt' and commit the result:" >&2
	diff -ru src "$tmp/src" || true
	diff -ru tests "$tmp/tests" || true
	exit 1
fi
echo "fmt-check: ok"
