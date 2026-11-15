#!/bin/sh
# Build vinci and put it on your PATH as a plain command.
# Usage: ./install.sh [bin directory, default ~/.local/bin]
set -eu

here=$(cd "$(dirname "$0")" && pwd)
bin=${1:-"$HOME/.local/bin"}
app="$HOME/.local/share/vinci"

dotnet test "$here/tests" --nologo -v q
dotnet publish "$here/src" -c Release -o "$app" --nologo -v q
mkdir -p "$bin"
ln -sf "$app/vinci" "$bin/vinci"

# The portal keeps running the old build until it is restarted.
systemctl --user try-restart portal.service 2>/dev/null || true

echo "Installed: $bin/vinci"
case ":$PATH:" in
  *":$bin:"*) ;;
  *) echo "Add $bin to your PATH to run it as 'vinci'." ;;
esac
