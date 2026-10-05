# Rusty Mines

## Minecraft status support

The TCP listener supports modern Java Edition status handshakes, an empty
Status Request (0x00), and Ping (0x01) with exactly eight payload bytes. It
returns framed JSON and echoes the ping bytes in Pong (0x01), then closes.
Ping is also accepted without a preceding Status Request, allowing latency-only
probes. Duplicate requests and malformed payloads are rejected. Fragmented and
coalesced TCP packets are supported. Workers poll reads at 250 ms; handshake,
status, and Login have five-second absolute phase deadlines, and writes have
five-second timeouts.

Status advertises protocol `777` with version name
`Rusty Mines 26.3 (offline; flat world)`, not the client's
protocol. Player maximum and online counts remain zero with an empty sample;
active TCP sockets are not players. The description is `Rusty Mines`.

## Offline Minecraft login

Java Edition 26.3 (protocol 777) supports Login Start → Login Success → Login
Acknowledged. Usernames must be 1–16 ASCII letters, digits, or underscores.
The server ignores the claimed UUID and derives the case-sensitive Java offline
UUID from MD5 of `OfflinePlayer:<name>` (without a namespace prefix). Login
Success includes an empty property list and a fresh UUIDv4 Session ID.
Unsupported login versions, transfers, malformed packets, and duplicate or
out-of-order login packets receive a Login Disconnect and the socket closes.

**Offline mode is insecure:** there is no Mojang account verification, so anyone
can impersonate another player's name. Traffic is unencrypted. Do not expose
this listener as an authenticated public server. The backend now records explicitly
**unverified offline profiles** through an authorized gateway; it does not authenticate
Minecraft accounts. Flat-world chunk streaming, bounded movement, read-only block
interactions, same-gateway player replication, restricted inventory/storage, and
unsigned System Chat are supported; see the implementation limits below.

After acknowledgement the wire transitions immediately to Configuration, including
when settings/brand packets are pipelined during backend confirmation. See below
for the completed Configuration exchange and flat-world chunk-window initialization.

The public library API `rusty_mines::login` provides both-direction login codecs,
including encryption, compression negotiation, plugin, and cookie packets.
These optional codecs do **not** enable encryption, compression, plugins, cookie
storage, Mojang HTTP requests, or transfers in the active server flow.

## Minecraft Configuration

For Java Edition **26.3 / protocol 777**, the gateway now sends its `minecraft:brand`
(`Rusty Mines`), the `minecraft:vanilla` feature flag, and the exact known-pack offer
`minecraft / core / 26.3`. It accepts strictly decoded client information and brand
updates throughout Configuration and stores the latest values in the local TCP
session, not additional database tables. Unknown plugin channels are ignored with
a 32767-byte payload bound; known brand payloads are validated as String(32767).

After the client echoes **exactly that core pack**, the gateway sends all **32
synchronized registries / 432 entries**, in the official server's numeric order,
and **15 tag registries / 773 tags** with official numeric IDs. Entry NBT is omitted
only because the official exporter proved every entry belongs to the negotiated
core pack. A missing, mismatched, duplicate, or extra pack is rejected with a
Configuration Disconnect: a full-NBT fallback for other clients is not implemented.
No minimal substitute registries or empty placeholder tag sets are sent.

Finish Configuration (`0x03`) waits for its empty acknowledgement, then enters
Play and sends the official-codec Play Login, loading event, default spawn,
creative-flight abilities, chunk center, a batch sized to the clamped client
view (radius at most 2), and an absolute teleport. The overworld spans y=-64..319;
the deterministic flat platform has bedrock at 62, stone at 63, and grass at
**64**, with spawn (8.5,65,8.5).
The dimension type, plains biome, block palettes, heightmaps, and light payloads
come from the pinned official 26.3 registry and packet codecs, not guessed IDs.
Chunk payloads are cached once; coordinates and the confirmed backend session's
positive entity ID are patched. As movement changes chunk coordinates, the worker
recenters at most every 50 ms, unloads chunks outside the capped window, and batches
new flat chunks under a 2 MiB cap. No terrain generation, filesystem access, or
persistence runs in a reducer.

Backend Play is confirmed only after both the exact protocol-777 teleport
confirmation (including coordinates/rotation) and either the empty Player Loaded
packet or the 60-server-tick loading fallback. The gateway uses nominal 50 ms
server intervals on its monotonic clock, starting at wire Play entry; Client Tick
End packets cannot accelerate loading. Idle polling also checks readiness.
`enter_play_session` is connection-authorized and permits Configuration →
Play or a matching Play retry while the lease remains live. The gateway waits for reducer success and its subscribed Play row; only
then can the existing dashboard count it. EOF, rejection, and failures retain
RAII cleanup. Player poses replicate through bounded, coalesced backend writes;
socket workers remain the only wire writers. The world is still read-only and has
no persisted block overrides or physics simulation.

On a published M2 module, session heartbeats run every five seconds in batches of at most 64, independently
of Minecraft traffic. Thirty-second leases cannot be revived by late heartbeats
or phase retries. Scheduled cleanup samples expiry every five seconds; subscribed
session removal, revocation, or backend unavailability closes workers on their
next poll. A rejected heartbeat batch may include a concurrently ended session,
so the next interval uses the current cache; transport/internal heartbeat failures
disconnect the gateway. Administrator enrollment establishes the cleanup schedule
for an upgraded database if it is missing. These host lifecycle behaviors still
require disposable-backend validation.

The gateway first checks the optional public lease-diagnostic view separately
from its required foundation subscriptions. Older published modules keep their
existing session lifecycle without receiving unknown heartbeat calls; the gateway
logs `Session leases unavailable; deployed module upgrade required`. This preserves
connectivity while making the missing expiry protection explicit. Lease support
requires publication of the reviewed M2 module; local compilation alone does not
upgrade the cloud database.

This is **not full gameplay**. Creative flight is the only initialized game mode;
game-mode changes, physics, Survival state, respawn, combat, and persistent world
edits are not implemented. Movement is accepted within the vanilla world border
and y [65,300], without collision authority. Finite movement beyond those bounds
receives an authoritative correction. Chunk interest follows accepted movement
in a radius-0-to-2 square, including unloads and negative-coordinate floor
division. Unknown/malformed packets cause an explicit Play Disconnect. Movement
received while a teleport is pending is ignored. Teleport IDs are positive
VarInts (including multi-byte IDs); corrections have one outstanding ID and a
ten-second acknowledgement deadline.

Basic normal controls no longer disconnect: empty Punch (`0x2E`), Player Command
(`0x2A`, actions 0–6), input flags (`0x2B`, mask `0x7F`), flight requests
(`0x28`, flying bit only), held hotbar slot (`0x36`, Short 0–8), block actions
(`0x29`, statuses 0–7), Use Item On (`0x42`) and Use Item (`0x43`, including
finite yaw/pitch). Held slot is **0x36**, verified with the official protocol-777
encoder; **0x35 is Set Beacon Effect**, not an older held-slot mapping.
Input and sprint state remain local. Player poses are coalesced to the backend at
50 ms intervals for same-authorized-gateway-identity replication. Attack, pick
block/entity, and recipe-book settings remain no-ops; container actions use the
restricted authoritative M3 inventory/storage path.

Breaking and placement/use-on remain read-only: authoritative base blocks are sent
before sequence acknowledgements, and no block mutation is committed. Creative
inventory writes, supported clicks, one simple storage menu, and a small crafting
set use authoritative inventory reducers and subscribed snapshots. The accepted
item/component policy is intentionally restricted; arbitrary items/effects and
complete vanilla menu parity are not claimed.

Welcome, operator broadcasts, notices, and player text use anonymous network-NBT
System Chat (`0x7C`, Overlay=false), not JSON or signed player chat. Player text
is explicitly unsigned, sender/session-authorized, rate-limited and scoped to the
gateway chat policy/history. `/help`, `/inventory`, and `/storage <UUID>` are the
implemented basic commands. Signed messages/commands are refused; no key, session,
signature, or secure-chat claim is verified. Run `broadcast` to interactively
enter 1–256 characters without terminal controls (Ctrl-C/D cancels). It targets
only local, connection-owned, backend-confirmed Play sessions from the current
snapshot. Each worker has a bounded 16-message queue independent of the priority
kick queue; workers recheck confirmed wire Play before sending. The console reports
successful socket writes, failures and unconfirmed timeouts—not client-read receipts.
Queue overflow never blocks workers or console; disconnect cleanup fails queued
requests and reconnects cannot inherit messages. No offline delivery/persistence.
Client settings, plugin messages, bounded untrusted chat-session metadata, tick end,
chunk-batch acknowledgements and matching keepalives remain supported.
Chat keys are not authenticated or used in this offline/insecure mode.

The backend exposes `admin_session_diagnostics` and `admin_lease_diagnostic`
views. They return data only to identities in the private Administrator table.
The session view is capped at the oldest 128 activity rows; its ages and lease
time remaining use the most recent scheduled-cleanup timestamp (at most five
seconds old). The lease summary reports total sessions, bounded expired rows,
and the cumulative count removed by expiry cleanup. It does not count normal
logout, gateway disconnect, or revocation as lease expiry. The local `gateway`
dashboard separately shows the current process's last bounded failure reason.

### Deferred connection controls

The active server flow does not send a Play Start Configuration request, cookie
request/store, transfer, or correlated Play Ping. It does not accept their
responses: unsupported Play packet IDs, including an unsolicited configuration
acknowledgement, receive a Play Disconnect and close the session. Transfer
handshakes receive a Login Disconnect. The status-listener Ping is a separate
Status-state feature and does not enable Play Ping/Pong.

Plugin messages are limited to the validated brand channel. Unknown serverbound
plugin channels are ignored after bounded decoding; no Play plugin messages are
sent. These policies can change only with a separately implemented and tested
request/response or state-transition flow.

World loading has a separate **30-second deadline**. Confirmed Play has no idle
phase deadline; keepalives continue every five seconds with a 15-second response
limit. Player Loaded and teleport confirmation are both required; there is no
fallback that counts a partially initialized client.

Workers use `Instant` outside reducers. Configuration's **30-second overall deadline**
starts at Login Acknowledged and includes backend confirmation, pack negotiation,
registry delivery, and finish acknowledgement. A Long keepalive uses elapsed
milliseconds, with a **5-second interval**, one outstanding request at a time, and a
**15-second response deadline**. Packet traffic and settings updates do not extend
these deadlines. Read polls are 250 ms rather than a five-second idle disconnect;
blocking backend confirmation is independently bounded at three seconds. Writes
retain a five-second per-operation timeout. Deadline checks occur between writes,
reads, and complete frames, so blocking I/O can delay enforcement by its timeout.

After a disconnect or completed status probe, the worker half-closes the write
side and drains input for at most **one second / 256 KiB**, with 50 ms read polls.
This lets the peer receive the reason and EOF without a reset from ordinary
pipelined unread data. A peer exceeding the drain bounds may still be reset when
the socket closes. Actual drain reads count bytes, but raw drained input does not
count as Minecraft frames. RAII backend cleanup also covers EOF and I/O failures.

`rusty_mines::configuration` exposes strict both-direction field codecs for client
information, plugin messages, known packs, keepalives, registry data with optional
anonymous network NBT, tags, feature flags, finish, and disconnect. NBT validates
bounded depth, element/byte counts, lengths, and Java modified UTF-8 strings; plain
text disconnects encode NUL and supplementary characters correctly. These codecs
do not add compression or online authentication.

Wire fields live in `src/packets/`: `play.rs` decodes supported Play packets,
validates official packet layouts, patches entity/chunk fields, and writes Play
frames through generic `Write`. Its public API is `rusty_mines::play`, alongside
Login and Configuration; it has no socket, backend, metrics, or console dependency.
`connection.rs` owns sequencing, deadlines, pending acknowledgements, movement
bounds, backend confirmation, and keepalive scheduling. `vanilla_world.rs` owns
asset loading/provenance, caching, and bounded client-centered chunk windows;
opaque official payloads remain unchanged except for packet-layer patches.

The checked-in asset and reproducible official extraction procedure, hashes, and
third-party notices are documented in [`assets/README.md`](assets/README.md).
Tests cover strict codecs, authoritative asset counts/order/IDs, packet phases,
absolute timers, backend-wait pipelining, byte/frame accounting, and graceful TCP
EOF. A graphical official 26.3 client has **not** been exercised; the loopback tests
validate the protocol exchange, not playable-client/world compatibility.

## Console world and gateway dashboards

| Command | Read-only live view |
| --- | --- |
| `world` | Pinned overworld metadata, initial/default spawn, current chunk-window cap, platform, creative flight, limitations and local backend-confirmed Play count |
| `gateway` | Endpoint/database, nonsecret SDK identity and owner connection ID, connected/subscription state, registration, authorization health, diagnostic generation, latest safe failure category and local Login/Configuration/Play counts |

Both use typed snapshots on the existing 50 ms console poll; rendering issues no
DB requests. Session counts reuse the same subscribed-cache/local-control snapshot
as `players`: other SDK connections and unmatched local controls are excluded, not
counted as online players. Unavailable registration is distinct from unregistered.
Diagnostic generation counts observed diagnostic changes, not reconnection attempts.
The latest failure category is retained through subsequent successful callbacks;
raw SDK errors, credentials and tokens are not exposed. It is not an error history.

Each command retains one plaintext snapshot in logs; live refreshes do not append
logs. Dashboard footers show `help · status · players · world · gateway · logs`
above, never instead of, the existing prompt. Completion includes both commands.
Default spawn is advertised metadata, **not an implemented respawn flow**. Static
metadata does not imply new gameplay. Network behavior, credentials/authentication,
backend schemas/bindings and dependencies are unchanged.

## Live players and local kick

`players` opens a live typed table in the existing dashboard, refreshed on its
50 ms input/render poll without network queries. It shows username, authoritative
subscribed Login / Configuration / Play phase, elapsed local connected duration,
and the latest **measured keepalive RTT**. `—` means no acknowledged sample; ping
is never estimated. Login/Configuration are joining sessions, not Play players.
A wire-Play client still shows Configuration until backend world-load confirmation.
Switching views retains logs and command snapshots; refresh/resize preserves input.

Only rows owned by this gateway's exact SDK connection **and** matched to a live
local session-UUID control are displayed. The backend view is identity-scoped and
can include other SDK connections; excluded nonlocal/unavailable rows are disclosed
but cannot be selected or kicked. Both Status's Play count and the players table
are restricted to backend-confirmed local sessions on this SDK connection.

`kick` suspends the dashboard using the same Reedline flow as `config`, lists current
local players with phases, accepts a number or unique username, then asks for a
reason (default `Kicked by server operator`). Ctrl-C/Ctrl-D cancels these prompts,
not the server. Reasons allow Unicode, quotes and backslashes, are limited to 256
characters, and reject terminal controls. Ownership/presence is checked again after
the prompts; the captured session UUID prevents a same-name reconnect being kicked.

A bounded per-worker channel accepts at most one kick per session. The worker
chooses Login JSON or Configuration/Play anonymous network-NBT text from its
**actual wire phase**, including phase races during a backend wait. No socket is
closed by the operator thread. Read polls are 250 ms; backend waits can add up to
three seconds and writes up to five seconds. Feedback says issued/pending until
the worker finishes its bounded half-close/drain and requests RAII backend cleanup;
it does not claim a backend deletion acknowledgement. After five seconds, pending
feedback is followed by a deferred completion/failure log. EOF and failures remove
local controls and retain exactly-once confirmed-session cleanup.

Kick is a one-session disconnect, **not a ban**, and does not add persistent gameplay
state. Offline names remain impersonable; this command provides no authentication.
There are no backend schemas, credentials, dependencies, or deployment changes.

## SpacetimeDB gateway foundation

Backend and client dependencies target `2.10.*`; the checked-in locks and generated
bindings use **2.10.2**, matching the installed CLI. `spacetime.json` currently selects
Maincloud and `rusty-mines-5gxbr`; `spacetime.local.json` repeats the database name.
The running gateway instead uses `rusty-mines.toml` (and the existing host override).
Do not assume CLI and runtime settings select the same database. Always specify the
server, database, and module path explicitly when deploying.

### Schema and trust boundary

`spacetimedb/src/lib.rs` loads host-backed tables/reducers from `foundation.rs` and
shared, natively testable validation/authorization rules from `policy.rs`.

| Private table | Purpose |
| --- | --- |
| `administrator` | Publisher/owner identity captured from `init`'s `ctx.sender()` |
| `gateway` | Explicit administrator-controlled allowlist of SDK identities |
| `player_profile` | UUID primary key, username, identity kind, first/last seen |
| `player_session` | Session UUID primary key, indexed player UUID/gateway identity, owning SDK connection, phase, connected/activity timestamps |

All profile/session timestamps come from `ctx.timestamp`. `begin_offline_session`
accepts **only a username and session ID**, validates the username, and derives the
Java offline UUID in the database; a caller cannot supply an authenticated player
identity or create a `Verified` profile. `Verified` is reserved for a future,
separately verified login path. Offline login cannot overwrite verified profiles.
Concurrent sessions for the same player UUID are rejected, never displaced.

`advance_login_session` allows only Login → Configuration; `enter_play_session`
allows only Configuration → Play after gateway-observed world readiness.
`end_session` is idempotent for an absent session. Mutations require an allowlisted
`ctx.sender()` and the session's exact `ctx.connection_id()`. Disconnect cleanup is
restricted to that connection, not every connection sharing its SDK identity.
Administrator `revoke_gateway` deletes all of the revoked gateway's sessions atomically.
Profiles survive cleanup and retain `first_seen`; `last_seen` advances on begin,
Configuration, Play, end, revocation, and gateway disconnect. A server restart relies on
SpacetimeDB's connection-disconnect lifecycle cleanup; there is no application timer
or expiry policy yet, and cleanup cannot be instantaneous during a network partition.

Only sender-scoped public views are subscribed: `my_gateway`, `gateway_sessions`,
and `gateway_profiles`. Unauthorized callers receive empty views; authorized gateways
see their own sessions and the profiles for those sessions (including other SDK
connections of the same identity). No client subscribes to authoritative private
tables. The original public `Person` table and `add`/`say_hello` remain compatible,
but are no longer subscribed by the gateway and confer no authorization.

Login Success is sent only after reducer completion **and** matching subscribed
session/profile data, with a three-second total confirmation deadline. The wire
Session ID is the database Session UUID. After Login Acknowledged, the gateway waits
for subscribed Configuration state, then starts the Configuration exchange above.
Backend failures before Success send Login Disconnect; failures after Acknowledged
use a Configuration Disconnect, matching the client's wire state. RAII cleanup covers
EOF, malformed/duplicate packets, failed writes, timeouts, normal close, and unwinding.
A begin request that times out may still commit, so its guard queues end on the same
SDK connection. Cleanup reducer failure disconnects the gateway to trigger lifecycle
cleanup. Process abort/forced termination relies on the host eventually detecting the
lost SDK connection. TCP/status probes never create player sessions.

The dashboard displays gateway authorization/subscription health. Players Connected
is counted from subscribed **Play** sessions for this gateway after world readiness, not
TCP sockets or Login/Configuration rows. Database connection/credential/subscription
failures disable login without terminating status handling or the console. There is
no automatic reconnect: fix the issue and restart the gateway.

### Publish and register a gateway

No publish, database reset, or server launch is performed by build/test commands below.
**Deployment must be done manually:** republish the backend to make
`enter_play_session` available before using this client. The pre-existing partial
world work appends a default-zero `player_session.entity_id` and private
`session_entity` allocation table; preserve migration data and reconnect sessions
created by an older module. The gateway's wire entity IDs are separately allocated
as process-local positive integers, not ordered database auto-increment IDs.
For a **new local development database**, with a local server already running:

```sh
spacetime login show
spacetime publish mines-dev --server local --module-path spacetimedb --delete-data=never
spacetime generate --lang rust --out-dir src/module_bindings --module-path spacetimedb --yes
```

Review migration prompts; never use `--delete-data=always` or `on-conflict` to deploy
this additive foundation. Keep the original `Person` data. Set the runtime TOML host
and database to that deployment, start `cargo run`, and find the **nonsecret**
`Gateway identity: <hex>` log entry. Using the administrator's CLI login, register it:

```sh
spacetime call mines-dev register_gateway '"<GATEWAY_IDENTITY_HEX>"' --server local
```

Subscribing before registration is safe; the authorization view updates after
registration without restarting. An anonymous gateway is never auto-allowlisted.
The CLI administrator login and the gateway token are separate credentials. On a
new database, 2.10.2 invokes `init` with the owner's identity; `ctx.identity()` would
be the **database** identity and must not be used as the owner authorization check.

**Existing template database migration:** `init` is not rerun on republish. For its
first foundation deployment only, explicitly bake the existing owner's hexadecimal
identity (from `spacetime login show`, after confirming database ownership) into the
module build. For example in PowerShell:

```powershell
$env:RUSTY_MINES_BOOTSTRAP_ADMIN = "<EXISTING_OWNER_IDENTITY_HEX>"
spacetime publish rusty-mines-5gxbr --server maincloud --module-path spacetimedb --delete-data=never
spacetime call rusty-mines-5gxbr register_gateway '"<GATEWAY_IDENTITY_HEX>"' --server maincloud
Remove-Item Env:RUSTY_MINES_BOOTSTRAP_ADMIN
```

This is a **build-time**, nonsecret setting, not a gateway environment variable.
With an empty administrator table, only that exact authenticated sender can bootstrap;
there is no first-caller/first-connection claim. A missing/invalid setting fails closed.
Once populated, the administrator table takes precedence and the bootstrap value is
ignored. Rebuild/republish without the setting when convenient; keep the private
administrator row. Only choose an identity controlled by the database owner. The module
cannot independently discover the owner during an ordinary reducer call. Review the
explicit Maincloud deployment yourself; these commands are not run automatically.

### Gateway token storage and recovery

The SDK receives a bearer token on its first connection and reuses it on later starts.
It is saved outside the project at the platform user config directory under
`rusty-mines/gateway-credentials/<host-and-database-hash>/token`; the actual path is
logged, never the token. The hash scopes credentials, not authentication. Unix creates
0700 directories/0600 files. Windows uses `whoami.exe` to identify the current account
and `icacls.exe` to remove inherited directory grants and grant that account access;
new token files inherit this protected directory ACL. Existing custom explicit ACLs
and the user's config directory remain the operator's responsibility. Restrict access
to the service account and administrators; do not share this directory or run from an
untrusted user profile. Tokens are stored as private plaintext bearer credentials,
not encrypted by an OS key store.

Writes are synchronized temporary-file writes with atomic **no-clobber** persistence.
Corrupt, oversized, unreadable, or Unix world/group-readable files do not fall back
to a new anonymous identity; different tokens are never silently overwritten. A
first-start race that produces different identities fails closed for the loser.
The SDK token is not a Minecraft player's credential and does not verify an offline
username. Use HTTPS except for trusted loopback development; the token is sent to the
configured SpacetimeDB host. No extra service or API key is introduced.

Restart normally with the same OS account, host, database, and saved token. After a
lost/invalid token or intentional rotation, stop the gateway, revoke the old identity
with administrator `revoke_gateway`, and move the affected credential file aside in
a secure location. Start again, then explicitly register the new logged identity.
Never paste bearer tokens into logs, source, TOML, or issue reports. Host/database
changes deliberately select a different token file and require registration there.

### Validation

```sh
cargo fmt --check
cargo test --all-targets
cargo check --all-targets
cargo clippy --all-targets -- -D warnings
cargo fmt --manifest-path spacetimedb/Cargo.toml --check
cargo test --manifest-path spacetimedb/Cargo.toml
cargo clippy --manifest-path spacetimedb/Cargo.toml --target wasm32-unknown-unknown -- -D warnings
spacetime build --module-path spacetimedb
spacetime generate --lang rust --out-dir src/module_bindings --module-path spacetimedb --yes
```

Bindings in `src/module_bindings` are generated **only** by the CLI, never hand-edited.
Native backend tests exercise the actual shared policy functions without linking the
SpacetimeDB WASM host. Client tests inject a test-only session store; production has
no offline/no-op backend bypass. Tests cover protocol framing, UUID correlation,
backend rejection, phase/write/EOF cleanup, pending-begin guards, and synthetic token
file handling without touching real credentials.

For real transactional/visibility testing, first publish this module to an explicitly
selected disposable `rusty-mines-test-*` database. Securely inject its administrator's
bearer token as `SPACETIMEDB_TEST_ADMIN_TOKEN`, and set `SPACETIMEDB_TEST_HOST` and
`SPACETIMEDB_TEST_DATABASE`. Then run:

```sh
cargo test --bin rusty_mines live_gateway_authorization_visibility_ownership_and_cleanup -- --ignored
```

This opt-in test checks unauthorized registration/login, private-table denial,
per-gateway visibility, duplicate-player rejection, cross-identity/cross-connection
ownership, connection-specific disconnect cleanup, phase restrictions, idempotent end,
and revocation. It makes ephemeral SDK identities and synthetic offline profiles,
revokes test gateways on exit, and never uses the gateway's saved token. Profiles
remain in the disposable database. It does not start a server, publish, reset, or delete
any database and is ignored by the normal suite. Do not use production admin secrets.

The M6/M7 world-action and player-state harness uses a separately published
disposable module/database with the same three environment variables. Run it with:

```sh
cargo test --bin rusty_mines live_world_actions_are_owned_sequenced_scoped_and_persistent -- --ignored
```

This harness checks sender/connection ownership, mode restrictions, exact retry,
concurrent edits, peer-scoped snapshots, inventory rollback at the chunk override cap,
profile vitals across a new session, world snapshot reconstruction after gateway
identity reconnect, and revocation. It edits fixed world coordinates and intentionally
verifies that overrides survive session cleanup; it does not clean those world rows up.
Use a disposable database only, and discard that database after the run. The harness
does not publish, reset, or delete a database.

## Interactive server console

Run `cargo run` from a terminal. No command-line flags are needed.

On first launch, the server asks where to store its TOML configuration:

- Enter `here` for `rusty-mines.toml` in the working directory.
- Enter a directory or an explicit `.toml` file path; paths with spaces are supported.
- Press Enter to use the platform's user configuration directory.

Setup then asks for the SpacetimeDB HTTP(S) address, database name, and TCP
listen address. Existing configuration files are loaded, never overwritten.
Custom locations are remembered in the user configuration directory. A
`rusty-mines.toml` in the working directory takes precedence over that selection.
Invalid or missing remembered configurations produce an error rather than
silently replacing them. Edit the TOML file and restart to change settings.
Set `SPACETIMEDB_HOST` in your environment or `.env` to override the TOML
`host` at runtime. It also supplies the default host during first-run setup.
The override must be a valid HTTP(S) address; existing TOML files are not modified.
Database and listen settings come from the saved TOML configuration.

Example `.env` entry:

```dotenv
SPACETIMEDB_HOST=http://localhost:3000
```

Example configuration:

```toml
host = "http://localhost:3000"
database = "mines-dev"
listen = "127.0.0.1:25565"
```

Once running, use the live console:

| Command | Purpose |
| --- | --- |
| `help` | List commands |
| `status` | Live fixed-layout listener, database, connections, uptime, and process metrics dashboard |
| `config` | Show saved settings and interactively edit host, database, or listen address |
| `logs` | Show retained history and follow new messages |
| `clear` | Clear the displayed view without deleting history |
| `stop` | Stop accepting clients, close active sockets, and disconnect from SpacetimeDB |

`config` displays the saved values and asks which item to edit (by name or
number), then asks for its new value. Invalid values can be retried; Enter keeps
the current value. Enter `done` to return to the console. Ctrl-C or Ctrl-D cancels
the editor without stopping the server. Each successful edit is saved immediately;
restart to apply it. The editor preserves comments and unrelated TOML fields.
`SPACETIMEDB_HOST`, when set, still overrides the saved host.

The running console uses a Ratatui/Crossterm alternate screen: logs appear above
an anchored bottom prompt and refresh within about 50 ms without a keypress.
Resizing reflows logs and keeps the input and cursor intact. `tui-input` provides
Unicode-aware editing and horizontal scrolling. Up/Down browse session command
history (Down restores your unfinished draft); Tab completes command names.
Ambiguous completions appear in a reserved row above the prompt, including while
status is open.
Page Up/Page Down scroll logs, and Ctrl-End returns to the latest output. New
messages automatically return the viewport to the bottom so updates stay visible.
Ctrl-C or Ctrl-D exits setup or stops the running server.

The console retains up to 4,096 log lines and 4,096 history entries in memory;
history is not persisted. Lines are limited to 8,192 characters and queued messages
to 32,768 characters. Background workers never block on output: the queue holds
1,024 messages and reports dropped messages when full. Terminal control characters
are removed from displayed logs. Every nonempty command replaces the displayed
view before showing its result. `clear` leaves retained logs intact; `logs` shows
them again and follows new arrivals. Command submissions and results (including
the initial status snapshot) are retained, but periodic status refreshes are not.
Background messages are retained without disrupting command output or status.

Setup and interactive `config` editing still use Reedline. During `config`, the
live screen is suspended and prompts/output appear in the normal terminal, not
in the retained log pane. Background logs queue until the editor returns; the
command view and input are then restored. The normal terminal is cleared before
config prompts. Settings are not hot-reloaded; restart to apply saved changes.
The listener/database remain running.
Terminal restoration and server shutdown run on UI errors and panic unwinding;
forced termination or aborting panics cannot guarantee cleanup.

A terminal supporting raw mode and alternate screens, and a running, published
SpacetimeDB database, are required for normal operation.

### Local live status metrics

`status` stays open until another command replaces it, polling snapshots every
50 ms. Ratatui diff rendering updates changed cells without clearing the terminal
on each tick; the bottom prompt, draft, cursor, command history, and completion
remain usable. The native dashboard groups SERVER, PROCESS (60s rolling avg),
and MINECRAFT TCP (since startup) into rounded panels using terminal palette
colors. Server/process panels sit side by side at 90 columns or wider, otherwise
stack. Labels and panel heights remain fixed as values change. Database and host
have distinct rows; long values end in an ellipsis rather than shifting fields.
Small screens clip panels safely while retaining the bottom prompt. RX/TX/Total
show human-readable binary byte units and Minecraft frame counts. Gateway states
(disconnected, subscribing, authorized, unauthorized) remain visible with a
login-unavailable warning when appropriate; detailed errors remain in logs.
Each explicit `status` command retains one matching plaintext snapshot in logs,
not one per refresh. Scope notes clarify process-only normalized CPU and traffic
exclusions. Wrapped log rows are cached by history generation and terminal
width rather than rewrapping all 4,096 lines every tick. CPU/memory sampling is
once per second; connection state and Minecraft traffic are read each poll.

`status` reads a cached snapshot; OS refreshes run only on a dedicated background
thread, once per second, using `sysinfo` 0.38.4. No external service or database
metrics are collected. The sampler starts before listener/database initialization.

- **CPU (60s rolling avg):** arithmetic mean of process CPU samples in the
  last 60 seconds (at most 60 samples). Process usage is divided by the local
  machine's logical processor count and clamped to 0–100%, so one fully occupied
  core on an eight-logical-CPU machine is 12.5%. The first refresh establishes the
  CPU baseline and is not counted as a fake zero sample.
- **Memory (60s rolling avg):** mean resident process memory, converted
  from bytes to **MiB** (1,048,576 bytes). The requested MB field explicitly
  displays MiB, not decimal MB. CPU and memory show no samples initially, then
  a concise `38/60 samples` window count (also shown after warmup).
  The count reflects actual samples within the last 60 seconds. These are sample means, not time-weighted means.
- **Minecraft TCP:** cumulative actual bytes read/written across this server's
  Minecraft client sockets since startup, including handshakes, status, login,
  and disconnects. Partial writes count only bytes successfully accepted by the
  socket; failed writes do not count a complete outbound packet. RX packets are
  complete decoded inbound Minecraft frames, and TX packets are complete written
  Minecraft frames, **not OS TCP datagrams**. Buffered socket reads are counted
  once at the socket boundary, including read-ahead. TCP/IP headers, retransmits,
  SpacetimeDB SDK traffic, DNS, and unrelated process/machine traffic are excluded.
  These are application-protocol counters, not total process network usage.
- **Players Connected:** counts subscribed Play sessions for the authorized gateway,
  currently zero because no connection reaches Play. Status probes, sockets, and
  successful offline Login/Configuration are not players. Future gameplay must
  explicitly authorize entry to/exit from Play before this field can change.

Only the current process's CPU/memory are OS-sampled; disks,
other processes, and system memory are not sampled. The snapshot/history lock
never encloses OS refreshes. An RAII guard wakes and joins the sampler on normal
shutdown, startup/UI errors, or panic unwinding (not process abort/forced kill).
OS refresh duration can still delay shutdown. OS process counter support and
permissions limit accuracy; unsupported counters may read zero.

## Original SpacetimeDB template guide

Get a SpacetimeDB Rust app running in under 5 minutes.

## Prerequisites

- [Rust](https://www.rust-lang.org/tools/install) installed
- [SpacetimeDB CLI](https://spacetimedb.com/install) installed

Install the [SpacetimeDB CLI](https://spacetimedb.com/install) before continuing.

---

## Create your project

Run the `spacetime dev` command to create a new project with a Rust SpacetimeDB module.

This will start the local SpacetimeDB server, compile and publish your module, and generate Rust client bindings.

```bash
spacetime dev --template basic-rs
```



## Explore the project structure

Your project contains both server and client code.

Edit `spacetimedb/src/lib.rs` to add tables and reducers. Use the generated bindings in `src/module_bindings/` to build your client.

```
my-spacetime-app/
├── spacetimedb/             # Your SpacetimeDB module
│   ├── Cargo.toml
│   └── src/
│       └── lib.rs           # Server-side logic
├── Cargo.toml
├── src/
│   ├── main.rs              # Client application
│   └── module_bindings/     # Auto-generated types
└── README.md
```



## Understand tables and reducers

Open `spacetimedb/src/lib.rs` to see the module code. The template includes a `Person` table and two reducers: `add` to insert a person, and `say_hello` to greet everyone.

Tables store your data. Reducers are functions that modify data — they're the only way to write to the database.

```rust
use spacetimedb::{ReducerContext, Table};

#[spacetimedb::table(accessor = person, public)]
pub struct Person {
    name: String,
}

#[spacetimedb::reducer]
pub fn add(ctx: &ReducerContext, name: String) {
    ctx.db.person().insert(Person { name });
}

#[spacetimedb::reducer]
pub fn say_hello(ctx: &ReducerContext) {
    for person in ctx.db.person().iter() {
        log::info!("Hello, {}!", person.name);
    }
    log::info!("Hello, World!");
}
```



## Test with the CLI

Open a new terminal and navigate to your project directory. Then use the SpacetimeDB CLI to call reducers and query your data directly.

```bash
cd my-spacetime-app

# Call the add reducer to insert a person
spacetime call add Alice

# Query the person table
spacetime sql "SELECT * FROM person"
 name
---------
 "Alice"

# Call say_hello to greet everyone
spacetime call say_hello

# View the module logs
spacetime logs
2025-01-13T12:00:00.000000Z  INFO: Hello, Alice!
2025-01-13T12:00:00.000000Z  INFO: Hello, World!
```

## Next steps

- See the [Chat App Tutorial](https://spacetimedb.com/docs/tutorials/chat-app) for a complete example
- Read the [Rust SDK Reference](https://spacetimedb.com/docs/clients/rust) for detailed API docs
