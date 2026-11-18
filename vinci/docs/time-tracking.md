# Time and work, counted for you

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

Every time vinci runs, it first asks ActivityWatch which windows had the focus since the last time, drops the time you were away from the keyboard, asks git for new commits, and writes the facts. So every command shows current totals. `vinci sync` prints what was just written.

- **It lags a few minutes while you work.** The watcher only confirms you were present after its idle timeout (3 minutes), so vinci counts up to the last confirmed moment and picks up the rest next time. Nothing is lost.
- **If ActivityWatch is not running**, vinci works as before and adds no time. ActivityWatch keeps its own record, so the time is added the next time both are up.
- **Time away from the screen** is `vinci spent i1 <minutes>`.
- **When several watches match one window**, the longest text wins, since it is the most specific.
- **A Build idea with no home and no watch** is a finding in `vinci review`.
- **`VINCI_NO_TRACK=1`** turns the automatic count off for a command, and `vinci sync` then prints nothing. The C# oracle and the differential tests use it.

## What a sync writes

| Kind | Written when | Fields |
| --- | --- | --- |
| `spent` | a watched window had the focus in the period, and the rounded minutes are above zero | `value` (minutes, two places), `where: "window"`. Dated at the end of the window's last stretch |
| `used` | a `use` watch matched at least a minute, the idea has shipped, and no `used` fact has the same local day | `text: "<span> on screen"`, `where: "window"` |
| `work` | a commit under a home is new (the idea and the hash are not recorded yet) | `text` (the subject), `where` (the full hash). Dated at the commit |
| `sync` | some watch was credited (even one that rounds to zero), or the gap since the last sync is more than a day | dated at the end of the period |

`vinci sync` prints one line per idea: `i1   +1h3m  vinci` for time, `i1   used  Glider` for use, `i1   27 commits  vinci` for work. When nothing was credited it prints `No watched window since 2026-11-18 09:00.`. When ActivityWatch does not answer it prints `ActivityWatch did not answer, so no window time was added. It will be picked up next time.`, and commits are still read.

## How the port counts (M5 step b)

The code is `mojo/src/track.mojo`, a port of `src/Tracker.cs`. Its reads from outside (the ActivityWatch query, `git log`, the machine's UTC offset, the case folding) are in `mojo/py/vinci_edge/track_edge.py`. Program.cs runs the tracker before any command that App.Run dispatches, so `vinci sync` and every other journal command write the same facts. The Mojo-only commands (`index`, `recall`, `open`, `feed`) and `serve` do not sync, since the C# had no such commands and `serve` returned before the tracker.

The query is the C#'s own: window events intersected with the not-afk periods, and the afk record for the end of the period. The period starts at the last sync, or at the first watch or home when there has never been one.

### Facts the C# binary showed (probed on the oracle, America/Chicago)

These were measured with a fake ActivityWatch on a free port. Mojo matches each one.

- **Timestamps keep seven fraction digits.** A stamp with nine (`16:00:00.123456789Z`, which the current aw-server-rust writes) is cut to seven (`.1234567`), not rounded.
- **Durations become whole 100 ns ticks by truncation.** `TimeSpan.FromSeconds` cuts the product: 3.7e-7 s is 3 ticks, 3.5 ticks is 3, and 1.23456789 s is 12345678 ticks. Not milliseconds: 0.0005 s stays 5000 ticks.
- **Minutes are `Math.Round(minutes, 2)`, which rounds a half to even.** 7.5 s (0.125 min) gives 0.12. 22.5 s (0.375 min) gives 0.38.
- **Window matching is `OrdinalIgnoreCase`, which folds non-ASCII letters.** `über` matches `PROBE ÜBER`. `straße` does not match `STRASSE`, since the German sharp s has no one-letter uppercase. Mojo folds with each character's simple uppercase, in one call to Python per list (`track_edge.fold_upper`).
- **Stamps are written as seven digits in the request.** The query's period is `2026-11-18T16:00:00.0000000Z/…`, with the zeros kept.
- **The longest watch wins, and watches on one idea share one total.** Two watches on the same idea credit one `spent` fact with the last end of the time.
- **A `used` fact compares local days** of the uses already in the journal against the day of the last window stretch.
- **The sync stamp is the end of the period**, which is the watcher's `through` (the end of the last afk record) or now, whichever is earlier.

### Checks

`mojo/py/vinci_diff/sync_diff.py` runs ten cases on the oracle and on Mojo, with the same clock, a fake ActivityWatch in-process (`fake_activitywatch.py`) or a closed port, and git repositories built with fixed dates. After every step it compares the exit code, stdout, stderr, the journal's bytes and the queries the server received:

```sh
cd mojo/py && uv run python -m vinci_diff.sync_diff --candidate ../build/vinci
uv run python -m vinci_diff.sync_diff --list          # the cases
```

The cases cover: no server (connection refused), empty buckets (and a day's gap), afk gaps with a window cut by afk, nanosecond stamps, offsets, a null title, windows across local midnight, one `used` a local day, a use on an unshipped idea, the longest watch, folded non-ASCII titles, commits under nested homes, a retired home, a tab and a non-ASCII letter in a subject, failed reads followed by a good one, rounding and a tick, nothing watched, and `VINCI_NO_TRACK=1`. Several cases repeat a sync at the same clock time, and that second sync must add nothing.

On the real ActivityWatch data (scratch copy of the staging journal, clock pinned to `2026-11-18T12:20:00-06:00`), both binaries printed the same two lines and wrote the same journal bytes: one `spent` of 63.07 minutes and one `sync`, and 27 `work` facts for commits from 2026-11-16 to 2026-11-18 that the journal never recorded (M2 ran with tracking off).

## When the afk record is wrong

The time away is cut by ActivityWatch's afk record, not by vinci. On this machine the afk watcher wrote no event from 2026-11-17 05:26 UTC until 2026-11-18 18:18 UTC. Its last `afk` event is open-ended and has a duration of about 36 hours, while the window watcher recorded about 33 hours of focus inside that span. Both the tracker and the activity feed drop that time, because the afk record says the person was away. Nothing in vinci is wrong with that rule. The record is. Look at the afk watcher in `deploy/awatcher.service` (`systemctl --user status awatcher`) before trusting old totals.

## Setting up ActivityWatch

On Wayland (Hyprland, Sway) use [awatcher](https://github.com/2e3s/awatcher), which bundles the ActivityWatch server with a window and idle watcher that work there:

```sh
yay -S awatcher-bundle-bin
cp deploy/awatcher.service ~/.config/systemd/user/
systemctl --user enable --now awatcher
```

vinci reads `http://127.0.0.1:5600`. Set `VINCI_ACTIVITYWATCH` to point it somewhere else. The activity feed (`vinci feed run activity`) reads the same server, see [feeds.md](feeds.md).
