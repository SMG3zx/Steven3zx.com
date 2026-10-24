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
