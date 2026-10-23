# Janus web

The Janus frontend: Qwik City pages rendered on the server under Bun. It has no database of its own. Every read and write goes through the gateway's JSON API (`janus/janus-go`), and the visitor's session cookie is forwarded to it.

## Configuration

| Variable        | Meaning                                                                               |
| --------------- | ------------------------------------------------------------------------------------- |
| `JANUS_API_URL` | The gateway's internal address, for example `http://gateway:9090`                     |
| `JANUS_WEB_URL` | The public address the site is served from, for example `https://janus.steven3zx.com` |
| `PORT`          | The port to listen on (default `3000`)                                                |

## Run

```bash
bun install
JANUS_API_URL=http://127.0.0.1:9090 JANUS_WEB_URL=http://localhost:3000 bun run dev
```

Production build and start:

```bash
bun run build.client && bun run build.server
JANUS_API_URL=http://gateway:9090 JANUS_WEB_URL=https://janus.steven3zx.com bun serve
```

## Tests and types

```bash
bun test
bunx tsc --noEmit
```

## CI and deployment

`.gitea/workflows/janus-web-ci.yml` runs on the self-hosted Gitea runner for every push that touches `janus/web`: install, `bun test`, `bun run build.types`, `bun run lint`, `bun run fmt.check`, and the client and server build. On `master` it then builds the image, and restarts the `web` container only if one already exists in the `janus-go` compose project. A push never starts a stack that is not running. Run the same checks locally with the commands above plus `bun run lint` and `bun run fmt.check`.

## Docker

`docker build -t janus-web:local .` builds the image. The `web` service in `janus/janus-go/docker-compose.yml` runs it under the `services` profile, with a `/healthz` health check.
