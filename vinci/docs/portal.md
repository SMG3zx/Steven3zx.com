# The portal

`vinci serve` is a page of your saved links on `http://127.0.0.1:4000`, with search, tag filters and a screenshot of each one. Three kinds of link appear on it.

- **Bookmarks** are facts in the journal:

  ```sh
  vinci journal bookmark "Qwik Docs" https://qwik.dev/ "Official documentation" +docs +qwik
  vinci journal bookmarks
  vinci journal unbookmark b1
  ```

- **Project links.** A project lists its own links in a `portal.json` at its root, an array of `{ "title", "url", "description"?, "tags"? }`. vinci reads every tracked `portal.json` in each repository an idea lives in, so a new project's links appear once the file is committed. Entries without a title or an http address are skipped, and `cold-storage/` folders are never read.
- **Local pages.** Any web page this machine is serving appears by itself, tagged `local`. vinci reads the listening ports from `/proc/net/tcp`, asks each one for its front page, and keeps those that answer with HTML. A scan is reused for 30 seconds, so a tool shows up within about a minute of starting. A saved link to the same port replaces the discovered one, and the portal does not list itself.

The first link to a URL wins. Screenshots are taken in the background with headless Chromium the first time a link is shown, and kept in `previews/` beside the journal; until then a placeholder is shown. Delete a picture to have it taken again. Set `CHROMIUM` if the browser is not at `/usr/bin/chromium`.

The page reads the journal on every request, so nothing needs restarting when a bookmark is added. To keep it running:

```sh
cp deploy/portal.service ~/.config/systemd/user/
systemctl --user enable --now portal
```

`./install.sh` restarts that service so it runs the new build.
