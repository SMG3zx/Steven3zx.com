#!/bin/sh
# Check and install vinci (the Python build) as a plain command.
# Usage: ./install.sh [bin directory, default ~/.local/bin]
#
# VINCI_APP_DIR sets the app directory (default ~/.local/share/vinci). A run with VINCI_APP_DIR set
# is a scratch install: it does not restart the portal service.
#
# The app directory holds:
#   vinci        the launcher (a shell script). The bin entry links here.
#   py/          the packages the command imports: vinci_core, vinci_cli, vinci_edge.
# The launcher pins the python3 this install found (3.13 or later) and sets PYTHONPATH to py/,
# so the command runs from any directory, without uv or the checkout.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
bin=${1:-"$HOME/.local/bin"}
app=${VINCI_APP_DIR:-"$HOME/.local/share/vinci"}

case "$app" in
  ""|/|"$HOME") echo "install: refusing app directory '$app'" >&2; exit 1 ;;
esac

# The test suite, through uv, as the gate runs it. A failing test stops the install.
(cd "$here/py" && uv run --group dev python -m pytest -q tests)

py_exe=$(command -v python3 || true)
[ -n "$py_exe" ] || { echo "install: no python3 on PATH" >&2; exit 1; }
"$py_exe" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 13) else 1)' \
  || { echo "install: $py_exe is older than 3.13" >&2; exit 1; }

# Stage the new app directory beside the old one, then swap it in.
stage="$app.new"
rm -rf "$stage"
mkdir -p "$stage/py"
for pkg in vinci_core vinci_cli vinci_edge; do
  tar -C "$here/py" --exclude=__pycache__ -cf - "$pkg" | tar -C "$stage/py" -xf -
done

cat > "$stage/vinci" <<LAUNCHER
#!/bin/sh
# vinci (Python build). Written by install.sh from $here; rerun install.sh to change it.
app=\$(dirname "\$(readlink -f "\$0")")
export VINCI_SELF="\$app/vinci"
export PYTHONPATH="\$app/py\${PYTHONPATH:+:\$PYTHONPATH}"
exec '$py_exe' -m vinci_cli "\$@"
LAUNCHER
chmod 755 "$stage/vinci"

rm -rf "$app"
mv "$stage" "$app"

mkdir -p "$bin"
ln -sf "$app/vinci" "$bin/vinci"

# The portal keeps running the old build until it is restarted. A scratch install leaves it be.
if [ -z "${VINCI_APP_DIR:-}" ]; then
  systemctl --user try-restart portal.service 2>/dev/null || true
fi

echo "Installed: $bin/vinci -> $app/vinci"
case ":$PATH:" in
  *":$bin:"*) ;;
  *) echo "Add $bin to your PATH to run it as 'vinci'." ;;
esac
