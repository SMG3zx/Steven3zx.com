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

## Docker

`docker build -t janus-web:local .` builds the image. The `web` service in `janus/janus-go/docker-compose.yml` runs it under the `services` profile, with a `/healthz` health check.
