#!/bin/sh
# Check and build vinci (the Mojo port), then install it as a plain command.
# Usage: ./install.sh [bin directory, default ~/.local/bin]
#
# VINCI_APP_DIR sets the app directory (default ~/.local/share/vinci). A run with
# VINCI_APP_DIR set is a scratch install: it does not restart the portal service.
#
# The app directory holds:
#   vinci        the launcher (a shell script). The bin entry links here.
#   vinci.bin    the Mojo binary.
#   lib/         the Mojo runtime libraries, copied out of mojo/.venv.
#   py/vinci_edge  the Python edge the binary imports (standard library only).
# The launcher pins the interpreter that uv run uses and points the binary at lib/ and py/,
# so the command runs from any directory, without uv, and whatever python3 is on PATH.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
bin=${1:-"$HOME/.local/bin"}
app=${VINCI_APP_DIR:-"$HOME/.local/share/vinci"}

case "$app" in
  ""|/|"$HOME") echo "install: refusing app directory '$app'" >&2; exit 1 ;;
esac

# Runs `make check` (format, build, tests, Python lint and types) and leaves build/vinci.
make -C "$here/mojo" check

# The interpreter and libpython that `uv run` uses in mojo/.venv.
interp=$(unset VIRTUAL_ENV; cd "$here/mojo" && uv run python -c \
  'import os, sys, sysconfig; print(sys.executable); print(os.path.join(sysconfig.get_config_var("LIBDIR"), sysconfig.get_config_var("INSTSONAME")))')
py_exe=$(printf '%s\n' "$interp" | sed -n 1p)
py_lib=$(printf '%s\n' "$interp" | sed -n 2p)
[ -x "$py_exe" ] || { echo "install: no Python at $py_exe" >&2; exit 1; }
[ -f "$py_lib" ] || { echo "install: no libpython at $py_lib" >&2; exit 1; }

# Stage the new app directory beside the old one, then swap it in.
stage="$app.new"
rm -rf "$stage"
mkdir -p "$stage/lib" "$stage/py"
cp "$here/mojo/build/vinci" "$stage/vinci.bin"
# The runtime libraries the binary links from the venv (its RUNPATH names the venv).
ldd "$here/mojo/build/vinci" | awk '$3 ~ /modular\/lib\// { print $3 }' | while read -r lib; do
  cp "$lib" "$stage/lib/"
done
tar -C "$here/mojo/py" --exclude=__pycache__ -cf - vinci_edge | tar -C "$stage/py" -xf -

cat > "$stage/vinci" <<LAUNCHER
#!/bin/sh
# vinci (Mojo build). Written by install.sh from $here; rerun install.sh to change it.
app=\$(dirname "\$(readlink -f "\$0")")
export LD_LIBRARY_PATH="\$app/lib\${LD_LIBRARY_PATH:+:\$LD_LIBRARY_PATH}"
export MOJO_PYTHON='$py_exe'
export MOJO_PYTHON_LIBRARY='$py_lib'
export VINCI_MOJO_PY="\$app/py"
exec "\$app/vinci.bin" "\$@"
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
