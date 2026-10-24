# Janus on Debian (WSL2)

Everything that used to run under Docker Desktop runs here instead.

| What | How | Ports |
|---|---|---|
| Janus services (gateway, identity, projects, builds, runs, fleet, audit) | `janus.service`, one process, embedded NATS | 9095 |
| Janus web | `janus-web.service` (Bun) | 3001 |
| Builds service sandbox | rootless Podman as the `janus` user | |
| Portfolio, admin, Headscale, Gitea, runners, ddns | `janus-edge.service`: `docker compose up -d` against `edge/compose.yaml` on **rootful** Podman | 8190, 8191, 8088, 3010 |
| Grafana, Loki, Tempo, Pyroscope, Prometheus, Jaeger, OTel collector | `janus-observability.service`: compose, services named explicitly | 3000, 3100, 3200, 4040, 9091, 16686, 4317-4318 |

`setup.sh` installs everything once; `deploy.sh` builds the committed Janus binary and web
bundle and restarts them. The compose projects are rebuilt with `docker compose up -d --build <service>`
from `edge/` or `janus/janus-go/`.

Notes:
- The Windows Traefik reaches these through WSL's localhost relay. Container ports published on
  127.0.0.1 appear on Windows as 127.0.0.1; Janus listens dual-stack, so it appears as `[::1]:9095`.
- Gitea's SSH port is on loopback only, not the tailnet address, because WSL cannot bind it.
- The admin app's Windows host agent listens on Windows loopback, which WSL containers cannot reach.
- WSL must be running for any of this to be up: something has to start the distro at logon.

## Deploy from Gitea

`.gitea/workflows/janus-deploy.yml` tests janus-go on every master push that touches it, then deploys
with `ci/janus-deploy.sh`. The deploy job runs on a host runner (`janus-runner.service`, label
`janus-host`) because the Janus services are systemd units on this distro, not containers.

- `ci/janus-deploy.sh` is installed as `/usr/local/sbin/janus-deploy`. It takes no arguments.
  It fetches master from Gitea into `/var/lib/janus-ci/repo` and runs that clone's `deploy.sh`,
  then waits up to 120s for `/healthz` and prints the journal if it does not answer.
- The runner user `gitea-runner` may run that one command as root (`/etc/sudoers.d/janus-deploy`).
  The job cannot choose the ref or the path, so a workflow file cannot make root build anything else.
- Gitea is private, so the fetch needs a read-only token. Create one for an account that can read
  the repository, and store it once: `install -m 0600 /dev/stdin /etc/janus/ci-token`. It is read by a
  credential helper, so it never appears in a command line or in the repository.

First-time setup, on this distro:

```bash
sudo deploy/wsl/setup.sh            # only if it has not run yet
sudo install -m 0600 /dev/stdin /etc/janus/ci-token   # paste the read-only token, then Ctrl-D
sudo GITEA_RUNNER_TOKEN='<registration token>' deploy/wsl/ci/setup-runner.sh
```

The registration token comes from the Gitea repository's Actions → Runners page. It is used once and not
written to disk. `setup-runner.sh` takes act_runner v0.5.0 from the `gitea/act_runner:0.5` image, the same one
the edge runners use.

The deploy job stays off until the repository variable `JANUS_DEPLOY` is `true`. Set it in Gitea
(repository → Settings → Actions → Variables) only after a manual run succeeds. Run the workflow once with
`workflow_dispatch` first, check that the test job passes, and check the deploy output before you enable
automatic deploys.

To roll back, revert the bad commit on master and let the workflow deploy the revert.

Not yet verified on the host: the runner registration, the sudo rule, and a live deploy. The wrapper's
logic is only checked by reading it and by `bash -n`.
