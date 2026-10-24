#!/usr/bin/env bash
# Deploys master from Gitea to this host. Installed as /usr/local/sbin/janus-deploy by
# setup-runner.sh, and run as root only through the sudoers rule for the runner user.
#
# It takes no arguments, so a job cannot choose what root builds: sudo allows this
# command with no arguments and nothing else. It fetches master into a root-owned clone,
# runs the deploy.sh from that clone, then waits for Janus to answer /healthz.
set -euo pipefail

remote=https://git.steven3zx.com/Steven3zx/Steven3zx.com.git
token_file=/etc/janus/ci-token
clone=/var/lib/janus-ci/repo
lock=/run/janus-deploy.lock

[ "$#" -eq 0 ] || { echo "janus-deploy takes no arguments" >&2; exit 2; }
[ "$(id -u)" -eq 0 ] || { echo "janus-deploy must run as root" >&2; exit 2; }
[ -r "$token_file" ] || { echo "missing $token_file: a read-only Gitea token, mode 0600" >&2; exit 1; }

exec 9>"$lock"
flock -n 9 || { echo "another deploy is running" >&2; exit 1; }

# The token is read by a credential helper at fetch time, so it never appears in argv.
# The \$ keeps the cat in the helper, not in this shell.
git_remote() {
  git -c credential.helper= \
    -c "credential.helper=!f() { echo username=janus-deploy; echo password=\$(cat $token_file); }; f" "$@"
}

install -d -m 0700 /var/lib/janus-ci
if [ ! -d "$clone/.git" ]; then
  git_remote clone --quiet "$remote" "$clone"
fi
git_remote -C "$clone" fetch --quiet origin master
git -C "$clone" reset --hard --quiet FETCH_HEAD
commit="$(git -C "$clone" rev-parse --short HEAD)"

echo "== deploying master at $commit"
bash "$clone/janus/janus-go/deploy/wsl/deploy.sh" "$clone"

# JANUS_HTTP_ADDRESS is 0.0.0.0:9095 in /etc/janus/janus.env (see setup.sh).
echo "== waiting for /healthz"
for _ in $(seq 1 40); do
  if curl -fsS --max-time 3 http://127.0.0.1:9095/healthz >/dev/null 2>&1; then
    echo "janus answers /healthz at $commit"
    exit 0
  fi
  sleep 3
done
journalctl -u janus -n 80 --no-pager >&2 || true
echo "janus did not answer /healthz within 120s at $commit" >&2
exit 1
