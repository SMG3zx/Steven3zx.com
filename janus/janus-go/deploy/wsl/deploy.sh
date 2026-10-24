#!/usr/bin/env bash
# Builds the committed state of the repo and restarts Janus.  Run as root:
#   deploy.sh [repo-root]      (default: the repo this script lives in)
# Deploys what is committed on HEAD, not the working tree.
set -euo pipefail
repo="$(cd "${1:-$(dirname "$0")/../../../..}" && pwd)"
build=/opt/janus/build
git config --global --add safe.directory '*'

rm -rf "$build"; install -d "$build"
git -C "$repo" archive HEAD janus/janus-go janus/web | tar -x -C "$build"

echo "== janus binary"
(cd "$build/janus/janus-go" && CGO_ENABLED=1 go build -trimpath -o /opt/janus/bin/janus.new ./cmd/janus)
install -d /opt/janus/bin
mv /opt/janus/bin/janus.new /opt/janus/bin/janus

echo "== web"
(cd "$build/janus/web" && bun install --frozen-lockfile && bun run build.client && bun run build.server)
rm -rf /opt/janus/web.new; install -d /opt/janus/web.new
cp -r "$build/janus/web/server" "$build/janus/web/dist" /opt/janus/web.new/
cp "$build/janus/web/package.json" /opt/janus/web.new/
rm -rf /opt/janus/web; mv /opt/janus/web.new /opt/janus/web
chown -R janus:janus /opt/janus

# The builds service looks for build/Dockerfile.wasm-* above its working directory.
rm -rf /var/lib/janus/build; cp -r "$build/janus/janus-go/build" /var/lib/janus/build
chown -R janus:janus /var/lib/janus

systemctl enable janus janus-web >/dev/null 2>&1
systemctl restart janus janus-web
sleep 3
systemctl --no-pager --lines=0 status janus janus-web | grep -E "●|Active"
