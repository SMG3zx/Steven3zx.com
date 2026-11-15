# Portal

A Qwik City page that lists saved links, with search, tag filters, and a screenshot of each site.

## Adding links

Links come from two places, merged by `scripts/collect-links.ts` into `src/generated-links.json`:

- **Hand-written links:** `links.manual.json` in this folder.
- **Project links:** any project can list its own links by adding a `portal.json` file at its root.
  It holds an array of links:

  ```json
  [
    {
      "title": "Janus",
      "url": "https://janus.steven3zx.com",
      "description": "Control plane and web UI.",
      "tags": ["janus", "tools"]
    }
  ]
  ```

  `title` and `url` are required. `description` and `tags` are optional. Only tracked files are read,
  and `cold-storage/` folders are skipped.

Titles must be unique, since each one names its preview image. Duplicate URLs are dropped.

## Local pages are found for you

Any web page served on this machine appears in the portal by itself, tagged `local`. Nothing has to be written down.

When the page is requested, `src/discover.ts` reads the listening ports from `/proc/net/tcp`, asks each one on
localhost for its front page, and keeps those that answer with HTML. The page's `<title>` becomes the link title.
A scan is reused for 30 seconds and refreshed in the background, so a newly started tool shows up within about a
minute and disappears the same way when it stops.

- A saved link that points at the same local port replaces the discovered one, so write one when you want a
  description, tags, or a screenshot. Discovered links show the placeholder image.
- The portal does not list itself.
- Only ports bound to loopback or to every address are checked.

## Commands

```sh
bun install
bun run dev            # local dev server
bun run links          # regenerate the link list
bun run previews       # screenshot links that do not have a preview yet
bun run previews -- --force   # recapture every preview
bun run build && bun run build.preview
bash scripts/deploy.sh # build, copy to ~/.local/share/portal, restart the service
```

## Service

`deploy/portal.service` is a systemd user unit that serves the built portal on `http://127.0.0.1:4000`.
`scripts/deploy.sh` installs it to `~/.config/systemd/user/`. Check it with `systemctl --user status portal`.

CI (`.gitea/workflows/portal-ci.yml`) runs the checks on every push that touches the portal or a `portal.json`,
and deploys from master.
