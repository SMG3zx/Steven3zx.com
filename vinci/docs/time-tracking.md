# Time and work, counted for you

> **Paused.** The Mojo port (mark 2) does not read ActivityWatch or git yet, so no window time or commit is written, and `vinci sync` prints nothing. The feeds come back in M5 (see [mark2-plan.md](mark2-plan.md)). Below is how the counting works, as the C# version did it, and what the M5 feeds keep.

There is no timer to start or stop, and you do not report that you worked. You say once where an idea lives:

```sh
vinci adopt ~/code/glider       # a folder that already exists becomes an idea, named after it
vinci home i1 ~/code/glider     # or give an idea you already have a home
```

A home does two things.

- **Editor windows count as time.** VS Code titles look like `Wing.cs - glider - Visual Studio Code`, so a home watches for `- glider -`. This works when the folder itself is what you opened. If you open a parent folder, the title names the parent, and you need a `watch` for a text that does appear.
- **Commits count as work.** Each new commit that changes a file under the home is recorded as a `work` fact with its subject and hash. That keeps the idea from being flagged as untouched. A newly homed idea takes only its latest commit, so the journal learns when it was last worked on without copying its history. A commit goes to the deepest home that holds one of its files.

For anything a home does not cover, watch for a text yourself:

```sh
vinci watch i1 "glider/README"         # work: time in this window counts for i1
vinci watch i1 "Glider Simulator" use  # use: this window is the shipped thing on screen
vinci watches                          # every watch in force
vinci unwatch i1 "glider/README"
```

The text is matched, ignoring case, against the app name and the window title together. Open `http://localhost:5600` and look at the Timeline to see what your titles look like.

A `use` watch records that you used what you shipped: at most one `used` fact a day, and only after a minute on screen and only once the idea has shipped. It never adds to the time worked.

Every time vinci runs, it first asks ActivityWatch which windows had the focus since the last time, drops the time you were away from the keyboard, asks git for new commits, and writes the facts. So every command shows current totals. `vinci sync` prints what was just written. (Paused, as above.)

- **It lags a few minutes while you work.** The watcher only confirms you were present after its idle timeout (3 minutes), so vinci counts up to the last confirmed moment and picks up the rest next time. Nothing is lost.
- **If ActivityWatch is not running**, vinci works as before and adds no time. ActivityWatch keeps its own record, so the time is added the next time both are up.
- **Time away from the screen** is `vinci spent i1 <minutes>`.
- **When several watches match one window**, the longest text wins, since it is the most specific.
- **A Build idea with no home and no watch** is a finding in `vinci review`.

## Setting up ActivityWatch

On Wayland (Hyprland, Sway) use [awatcher](https://github.com/2e3s/awatcher), which bundles the ActivityWatch server with a window and idle watcher that work there:

```sh
yay -S awatcher-bundle-bin
cp deploy/awatcher.service ~/.config/systemd/user/
systemctl --user enable --now awatcher
```

vinci reads `http://127.0.0.1:5600`. Set `VINCI_ACTIVITYWATCH` to point it somewhere else.
