#!/usr/bin/env bash
# Builds the portal, copies it to the service directory, and restarts the user service.
# Usage: bash scripts/deploy.sh
set -euo pipefail

cd "$(dirname "$0")/.."

DEST="${PORTAL_DEST:-$HOME/.local/share/portal}"
UNIT_DIR="$HOME/.config/systemd/user"
PORT=4000

bun install --frozen-lockfile
bun run build
bun run build.preview

mkdir -p "$DEST"
rsync -a --delete --exclude node_modules --exclude tmp --exclude .git ./ "$DEST/"
(cd "$DEST" && bun install --frozen-lockfile)

mkdir -p "$UNIT_DIR"
install -m 644 deploy/portal.service "$UNIT_DIR/portal.service"
systemctl --user daemon-reload
systemctl --user enable portal.service
systemctl --user restart portal.service

# Give the server a moment to start, then check it answers.
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if curl -fsS -o /dev/null "http://127.0.0.1:$PORT/"; then
    echo "portal is up at http://127.0.0.1:$PORT/"
    exit 0
  fi
  sleep 1
done

echo "portal did not answer on port $PORT" >&2
systemctl --user status portal.service --no-pager >&2 || true
exit 1
