#!/usr/bin/env bash
# One-time setup of the host runner that deploys Janus to this WSL distro. Run as root:
#   GITEA_RUNNER_TOKEN=<registration token> setup-runner.sh
# Before running it, put a read-only Gitea token in /etc/janus/ci-token (mode 0600). The
# registration token is used once and is not written to disk; act_runner keeps only its
# own runner credential, in /var/lib/gitea-runner/.runner.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
instance=http://127.0.0.1:3010
image=docker.io/gitea/act_runner:0.5

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
[ -n "${GITEA_RUNNER_TOKEN:-}" ] || { echo "set GITEA_RUNNER_TOKEN to a runner registration token from Gitea" >&2; exit 1; }
[ -r /etc/janus/ci-token ] || { echo "create /etc/janus/ci-token first: a read-only Gitea token, mode 0600" >&2; exit 1; }
command -v sudo >/dev/null || { echo "sudo is required for the runner's deploy rule" >&2; exit 1; }
command -v podman >/dev/null || { echo "podman is required to fetch act_runner; run setup.sh first" >&2; exit 1; }

# The binary comes from the same image the edge runners use, so the version matches.
podman run --rm --entrypoint cp -v /usr/local/bin:/out "$image" /usr/local/bin/act_runner /out/act_runner
chmod 0755 /usr/local/bin/act_runner

id gitea-runner >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/gitea-runner \
  --shell /usr/sbin/nologin gitea-runner
install -d -m 0700 -o gitea-runner -g gitea-runner /var/lib/gitea-runner

# The deploy wrapper is root-owned and takes no arguments; the runner user may run it and nothing else.
install -m 0755 "$here/janus-deploy.sh" /usr/local/sbin/janus-deploy
printf '%s\n' 'gitea-runner ALL=(root) NOPASSWD: /usr/local/sbin/janus-deploy ""' > /etc/sudoers.d/janus-deploy
chmod 0440 /etc/sudoers.d/janus-deploy
visudo -cf /etc/sudoers.d/janus-deploy >/dev/null

cat > /var/lib/gitea-runner/config.yaml <<'CONF'
log:
  level: info
runner:
  file: /var/lib/gitea-runner/.runner
  # One deploy at a time. janus-deploy also takes a lock, so this is belt and braces.
  capacity: 1
  timeout: 30m
CONF
chown gitea-runner:gitea-runner /var/lib/gitea-runner/config.yaml

if [ ! -f /var/lib/gitea-runner/.runner ]; then
  runuser -u gitea-runner -- act_runner register --no-interactive \
    --config /var/lib/gitea-runner/config.yaml \
    --instance "$instance" --token "$GITEA_RUNNER_TOKEN" \
    --name janus-wsl --labels 'janus-host:host'
fi

install -m644 "$here/janus-runner.service" /etc/systemd/system/janus-runner.service
systemctl daemon-reload
systemctl enable --now janus-runner
systemctl --no-pager --lines=0 status janus-runner | grep -E "●|Active" || true
echo "runner set up; the deploy workflow's janus-host job will use it"
