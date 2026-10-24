#!/usr/bin/env bash
# One-time setup of a Debian host (WSL2 or not) to run Janus without Docker Desktop.
# Run as root:  setup.sh [path-to-existing-janus.env]
# Installs Go, Bun and rootless Podman, creates the janus user and /etc/janus/janus.env,
# and installs the systemd units. deploy.sh then builds and starts everything.
set -euo pipefail
src_env="${1:-}"
GO_VERSION=1.27.3
BUN_VERSION=1.4.2

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq podman podman-docker build-essential git curl unzip ca-certificates \
  uidmap slirp4netns fuse-overlayfs crun passt netavark aardvark-dns >/dev/null
touch /etc/containers/nodocker   # silence the podman-docker "emulating docker" notice

if ! /usr/local/go/bin/go version 2>/dev/null | grep -q "go$GO_VERSION"; then
  curl -fsSL "https://go.dev/dl/go$GO_VERSION.linux-amd64.tar.gz" | tar -C /usr/local -xz
fi
ln -sf /usr/local/go/bin/go /usr/local/bin/go
if ! command -v bun >/dev/null; then
  curl -fsSL "https://github.com/oven-sh/bun/releases/download/bun-v$BUN_VERSION/bun-linux-x64.zip" -o /tmp/bun.zip
  unzip -qo /tmp/bun.zip -d /tmp
  install -m755 /tmp/bun-linux-x64/bun /usr/local/bin/bun
fi

id janus >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/janus \
  --shell /usr/sbin/nologin --add-subids-for-system janus
# Rootless Podman needs the user's runtime directory, which lingering keeps alive.
loginctl enable-linger janus
install -d -o janus -g janus /var/lib/janus /opt/janus /opt/janus/web

# Builder Dockerfiles use short image names such as oven/bun, as Docker would resolve them.
install -d /etc/containers/registries.conf.d
echo 'unqualified-search-registries = ["docker.io"]' > /etc/containers/registries.conf.d/docker.conf

# No systemd user session exists for the janus service, so Podman manages cgroups itself.
install -d -o janus -g janus /var/lib/janus/.config/containers
cat > /var/lib/janus/.config/containers/containers.conf <<'CONF'
[engine]
cgroup_manager = "cgroupfs"
events_logger = "file"
CONF
chown janus:janus /var/lib/janus/.config/containers/containers.conf

install -d -m 750 -o root -g janus /etc/janus
if [ ! -f /etc/janus/janus.env ]; then
  [ -n "$src_env" ] || { echo "no /etc/janus/janus.env yet: pass the path of an existing .env" >&2; exit 1; }
  {
    # Carry over the secrets and OAuth settings; the paths and addresses are fixed here.
    grep -E '^(JANUS_JWT_SECRET|JANUS_JWT_ISSUER|JANUS_JWT_EXPIRY|JANUS_COOKIE_SECURE|GOOGLE_[A-Z_]+|GITHUB_[A-Z_]+|HEADSCALE_[A-Z_]+)=' "$src_env" | tr -d '\r'
    echo 'JANUS_HTTP_ADDRESS=0.0.0.0:9095'
    echo 'JANUS_WEB_UPSTREAM=http://127.0.0.1:3000'
    echo 'JANUS_WASM_BUILDER_IMAGE=janus-rust-wasm-builder:local'
    echo 'JANUS_GO_WASM_BUILDER_IMAGE=janus-go-wasm-builder:local'
    echo 'JANUS_JS_WASM_BUILDER_IMAGE=janus-js-wasm-builder:local'
  } > /etc/janus/janus.env
  chown root:janus /etc/janus/janus.env
  chmod 640 /etc/janus/janus.env
fi

here="$(cd "$(dirname "$0")" && pwd)"
install -m644 "$here/janus.service" "$here/janus-web.service" /etc/systemd/system/
systemctl daemon-reload
echo "setup done; run deploy.sh next"
