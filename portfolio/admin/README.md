# Origin admin

`https://www.steven3zx.com/admin/` is a username/password sign-in that leads to a live React Flow map of the origin host. The map shows CPU per thread, memory, network (interfaces, listening sockets, inbound and outbound connections), storage, processes, Windows services, and Docker containers.

## Pieces

| Dir | What | Runs |
|---|---|---|
| `agent/` | Go collector for the Windows host. It serves `GET /snapshot` and `GET /logs/{id}` with a bearer token on `127.0.0.1:9180`. `/snapshot?topics=…` returns only the named parts. `/logs/{id}` streams a container's output as NDJSON. | Natively on Windows, as the service `steven3zx-host-agent`. The service runs `hostagent.exe -supervise`, which keeps the agent worker running and serves `POST /restart` on `127.0.0.1:9181` |
| `server/` | Go API: sign-in, sessions, the job tracker, and a WebSocket at `/admin/api/live`. The socket forwards each browser only the topics its open views draw, plus the log stream for the container whose Logs tab is open. Users live in a local Turso file. | `admin` container in `edge/compose.yaml` |
| `web/` | React + TypeScript + `@xyflow/react` UI, built into the `admin` image. Each container card has a Logs button. | Served by `server/` under `/admin/` |

Caddy routes `/admin` and `/admin/*` on `www.steven3zx.com` to `admin:8091`. The admin container reaches the agent at `host.docker.internal:9180` and the host supervisor at `host.docker.internal:9181`. The supervisor is what the Restart button calls, so it still answers when the agent is down.

## Security

- Passwords are stored as argon2id hashes (64 MiB, t=3, p=2). The minimum length is 12 characters.
- Sessions are 32 random bytes in an `HttpOnly; Secure; SameSite=Strict` cookie scoped to `/admin`. The database stores only the SHA-256 of the token. Sessions expire after 12 hours. Changing a password signs out every session for that user.
- POSTs must be JSON from the same origin.
- Failed logins are limited to 5 per 15 minutes per client IP (taken from `CF-Connecting-IP`) and 20 per 15 minutes per username.
- The agent listens only on loopback and requires `HOST_AGENT_TOKEN` from `edge/.env`.
- `admin.db` is encrypted at rest with Turso's AEGIS-256 page encryption (experimental upstream). The key is `ADMIN_DB_KEY` in `edge/.env`: 64 hex characters from `openssl rand -hex 32`. It never goes into the `admin_data` volume. Without the key, the file does not open. If the key is lost, the database is lost too, so keep a copy in a password manager.

## Database encryption

On start, the server checks `admin.db`. If it is still a plain SQLite file, the server converts it once. Turso cannot encrypt a file in place, so the server builds `admin.db.encrypting` by `ATTACH`ing the plain file and copying the users and sessions into it. It then renames the plain file to `admin.db.plain-backup` and puts the encrypted file in its place. Existing accounts and sessions keep working.

The backup is unencrypted and still holds the password hashes. Delete it once sign-in works:

```bash
docker run --rm -v steven3zx-edge_admin_data:/data busybox rm /data/admin.db.plain-backup
```

Turso has no rekey. To rotate the key, use the same `ATTACH` copy into a new file with the new key.

### Backups

Back up by **copying the encrypted file**: stop `admin`, then copy `admin.db` and any `admin.db-wal` out of the volume. The copy stays encrypted and needs the same `ADMIN_DB_KEY`.

**Never use `VACUUM INTO` for a backup.** From an encrypted connection, it writes a plaintext copy.

## Managing users

Turso locks the database file, so stop the server while you change users:

```bash
cd edge
docker compose stop admin
docker compose run --rm -it admin useradd steven   # or: passwd steven
docker compose start admin
```

`useradd` and `passwd` prompt for the password twice without echoing it. `docker compose run` passes `ADMIN_DB_KEY` to them, just as it does for the server.

## Agent

Run `pwsh portfolio/admin/agent/install.ps1` from an elevated PowerShell to build `agent/build/hostagent.exe` and install the Windows service `steven3zx-host-agent`. The service starts at boot, restarts itself after a crash, and is not tied to a terminal window, so closing a shell does not stop it. Run the script again after you change the agent. It reads `HOST_AGENT_TOKEN` from `edge/.env`. It also removes the scheduled task that earlier versions used.

The service runs as LocalSystem. If the agent's Docker data stops appearing after install, check that LocalSystem can reach `\.\pipe\docker_engine`.

The agent worker logs to the console when run by hand. Under the service, logs go to `agent/build/hostagent.log`.

To restart the agent from the admin map, use the **Restart host agent** button. It asks the supervisor on `127.0.0.1:9181`. To restart by hand, run `Restart-Service steven3zx-host-agent` from an elevated PowerShell.

## Local development

```bash
cd portfolio/admin/web && npm install && npm run dev        # Vite proxies /admin/api to :8091
cd portfolio/admin/server && HOST_AGENT_TOKEN=... ADMIN_DB_KEY=<64 hex> ADMIN_COOKIE_SECURE=false ADMIN_WEB_DIR=../web/dist go run .
```

Keep the same `ADMIN_DB_KEY` between runs, or `data/admin.db` will not open.
