# Native Windows edge

Traefik and CrowdSec run on Windows directly, not in Docker. Docker rewrites every client
address to its gateway (`172.19.0.1`), so a containerised Traefik cannot tell clients apart
and CrowdSec would ban the gateway. Natively, Traefik sees the real peer.

| Piece | Where it runs | Notes |
|---|---|---|
| Traefik | Windows service `steven3zx-traefik`, from `C:\steven3zx\edge\bin` | Config in `traefik/`, rendered by `render-config.ps1` |
| `svcwrap` | Wraps `traefik.exe` as a service and restarts it | Go, in `../svcwrap` |
| CrowdSec engine | Windows service `crowdsec` (MSI) | Local API on `127.0.0.1:8180`, not 8080 |
| Traefik bouncer | Plugin inside Traefik | `traefik/dynamic/crowdsec.yml`, IP bans only, stream mode |
| Windows Firewall bouncer | Windows service (Chocolatey) | Blocks banned addresses on the host firewall |

Backends stay in Docker. `compose.yaml` publishes them on loopback for Traefik:
portfolio `8190`, admin `8191`, Gitea web `3010`, Headscale `8088`.

## Install

From an elevated PowerShell:

```
pwsh edge/native/install.ps1            # install, leave Docker Traefik serving 80/443
pwsh edge/native/install.ps1 -Cutover   # also move 80/443 to the service
```

It needs `CLOUDFLARE_API_TOKEN` and `TAILNET_IP` in `edge/.env`. Re-run it after changing the
static config; routes in `traefik/dynamic` reload on their own.

## Rollback

```
Stop-Service steven3zx-traefik
docker compose --profile traefik-docker up -d traefik     # from edge/
```

## What protects what

- Public names (`www`, apex, `janus`, `git`, `plane`) use the `crowdsec` middleware.
- `ts.steven3zx.com` (Headscale) and `private.steven3zx.com` do not, so a ban cannot cut
  off the tailnet or your admin path.
- `private.steven3zx.com` is served only on port 8443, bound to the tailnet address.
- Cloudflare's ranges are trusted for `X-Forwarded-For` on 80 and 443 and nowhere else. They
  are listed in `traefik/traefik.template.yml`, `traefik/dynamic/crowdsec.yml` and
  `crowdsec/whitelist.yaml`; update all three if Cloudflare changes its list.
- The CrowdSec whitelist covers loopback, the tailnet, the LAN and Cloudflare, so none of
  them can be banned.

## Things to know

- The bouncer plugin checks its key file even when disabled. If
  `C:\steven3zx\edge\crowdsec-traefik.key` is missing, Traefik drops every route that uses
  the middleware and those sites return 404. `install.ps1` creates the key before it starts
  the service.
- Traefik cannot reopen its access log on Windows, so there is no rotation. The log only
  holds 400-599 responses, and `install.ps1` starts a fresh file each time it runs.
- AppSec (the WAF) is not enabled. Add it after the IP bans are proven.
