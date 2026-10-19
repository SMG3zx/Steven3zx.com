# Java 26.3 / protocol 777 Play packet roadmap

Reference and implementation review: **2026-10-04**. This document records packet behavior; implementation changes are described in the linked source and validation notes.

Fresh review: [progress/evidence report](progress-review-20261004T230800Z/report.md), [action queue](progress-review-20261004T230800Z/actions.json). Local commands passed at the captured checkpoint; subsequent local tests, Clippy, formatting and backend WASM check are recorded in the implementation plan. Jev source judgments do not promote packet or milestone acceptance, and one worker batch failed typed validation.

## Source, scope, and status

Authoritative inventory for this review: the freshly fetched [Minecraft Wiki packet tables](https://minecraft.wiki/w/Java_Edition_protocol/Packets#Play), identifying Java **26.3**, protocol **777**; fetched revision [3810839](https://minecraft.wiki/w/Java_Edition_protocol/Packets?oldid=3810839), last edited 2026-10-01. IDs below come from the **List of packets → Play** tables, including packets documented in shared-state sections. Do not substitute older cached tables: serverbound held-item selection is **0x36**, not 0x35. IDs are scoped to direction and state, not globally unique.

Attribution: packet names/IDs are referenced from Minecraft Wiki contributors and the page's wiki.vg lineage. The source page specifies [CC BY-SA 3.0 Unported](https://creativecommons.org/licenses/by-sa/3.0/); this document is distributed under that license. Implementation notes and plans are original summaries, not copied specification prose. Follow the linked specification for field-level layouts.

Scope: every current **Play** packet, clientbound (CB, server → client) and serverbound (SB, client → server). Handshake, Status, Login, and Configuration inventories are excluded; shared packets appear here only with their Play IDs. Optional means a feature may be deliberately deferred, not that its packet is implemented.

Legend:

- **I** — implemented for the explicitly stated current purpose; not a claim of complete vanilla gameplay.
- **P** partial: static asset, restricted policy, local-only state, envelope-only parsing, or accepted-and-discarded input. Acceptance is **not** feature completion. M1 hardening does not promote these rows to complete gameplay.
- **-** — not implemented in the current Play path; a planned feature, not an accepted packet. Unsupported/invalid Play input currently disconnects.

Each `(direction, ID)` has exactly **one inventory row**, in its owning functional batch. Batch 0 summarizes the baseline without repeating packet rows. Partial rows stay in their upgrade batch; references in prose do not count as additional inventory entries.

Implementation evidence: `src/packets/play.rs` (decode/write paths), `src/connection.rs` (session policy, bounded simulation and emission), `src/gateway.rs` and `spacetimedb/src/foundation.rs` (sender-scoped pose/chat state), `spacetimedb/src/world.rs` (persistent world/player state), and `src/vanilla_world.rs` (pinned initialization and chunk-stream assets). The deterministic flat platform has a movement-centered window capped at radius two, same-authorized-gateway player replication, M3-backed inventory/storage, and bounded unsigned System Chat. `/help`, `/inventory`, and `/storage` are supported. M6 has sender-scoped persistent override snapshots and callback-plus-subscription-confirmed edits for supported Survival/Creative surface operations; Adventure edits remain denied without supported permission components. New edits are capped at 128 per chunk and 65,536 per world. M7 has bounded mode/ability state, SpacetimeDB-persisted health/food/death with active-session ownership, vertical gravity/platform landing, border corrections, accepted-sprint food loss, and mode-correct same-dimension void death/respawn. No disposable DB reducer-mutation or graphical screenshot acceptance was run. Cross-gateway chunk interest, combat, secure signed chat, general command graph and suggestions are not claimed.

Local acceptance setup remains unresolved: the configured `rusty-mines-5gxbr` database is absent from the reachable local server, whose database list contains only `quickstart-chat`; no publish or reducer call was made. The ignored live M6 harness compiles with retry, same-gateway peer, cross-session concurrency, cap-triggered transaction rollback and revocation assertions, but was not run. A Minecraft 26.3 process is open, but Computer Use approval explicitly rejected access to `javaw`, so no screenshot was taken. The visual evidence step will use PyAutoGUI `screenshot()` scoped to the game window after that window is approved for inspection; [The documented API](https://pyautogui.readthedocs.io/en/latest/screenshot.html) returns a PIL image and accepts `region=(left, top, width, height)` for a window-bounded capture.

## Acceptance evidence review — 2026-10-04

The initial local inventory audit counted 213 rows with 213 unique `(direction, ID)` keys and no duplicates: I=25, P=38, unimplemented=150 at that review snapshot. This paragraph records the dated acceptance-evidence review, not the current aggregate; later verified source increments are reflected in Batch 0 and the final recount below. Implemented coverage is partial gameplay coverage, not full acceptance. The erroneous 215-row total was corrected from the actual rows; no packet status was promoted by the review itself.

The Rust reviewer ran an authorized eight-question, source-fingerprinted Jev review using bounded excerpts and pinned `jev-1.13.0`. It contradicted claims of full codec/live-backend/M7 graphical acceptance; baseline and commit-confirmation judgments had low confidence, while atomicity and restart persistence required more evidence. Independent inspection confirms same-reducer inventory/block writes and matching reducer/subscription confirmation for successful edit ACKs, not live transaction or graphical acceptance. See [acceptance-evidence-review.md](acceptance-evidence-review.md) for probabilities and limitations. This review ran no gameplay, live-backend or graphical tests and does not close acceptance gates.

Granular follow-up evaluated 32 narrow source/acceptance claims in four requests. It supports backend edit safeguards and successful callback/subscription ACK gating, while reload ordering and live/graphical evidence remain unresolved. Direct inspection rejected a low-confidence all-mode synchronization concern and found a partial death/Respawn source increment that still needs readiness/inventory/fixture and acceptance review. See [acceptance-evidence-granular-review.md](acceptance-evidence-granular-review.md). No packet status was promoted from these judgments; the earlier inventory audit is a snapshot, not an automatic recount of concurrently evolving source.

## Batch 0 — existing baseline (summary only)

| Direction | Current surface | I | P | Remaining (-) | Total |
| --- | --- | ---: | ---: | ---: | ---: |
| CB | 33 distinct IDs marked I/P | 21 | 12 | 111 | 144 |
| SB | 33 distinct IDs marked I/P | 4 | 29 | 36 | 69 |
| Both | 66 distinct direction-scoped IDs marked I/P | 25 | 41 | 147 | 213 unique IDs; 213 inventory rows |

CB disconnects, keep-alives, system messages, block acknowledgements, mode/health state, and inventory snapshots/menu lifecycle work for their stated purposes. Static initialization assets, bounded terrain, and restricted block mutation remain partial, even when wire bytes are official-codec-derived. SB initial teleport confirmation and matched keep-alive responses work for their current purpose; bounded mode/flight requests use server policy. Inventory, storage and supported world mutations run through backend reducers and authoritative subscriptions, without implementing all vanilla container, crafting or item-component behavior. Every discard-only handler remains P. Acceptance is conditional on payload validation/session state, not blanket acceptance of arbitrary bytes.

Known baseline gaps to preserve in planning:

- Player Session structurally bounds the public key to **512 bytes** and signature to **4096 bytes**. The session data is discarded; no key/session/chat cryptographic verification exists.
- Loading uses a **60-server-tick fallback**, approximated by monotonic 50 ms intervals until a simulation loop exists. Client Tick End cannot advance it; Player Loaded remains supported. Loading still requires the initial teleport acknowledgement, and no explicit client smoke test has been recorded.
- Horizontal position/rotation begins with offline client input but is bounded per packet, clamped to the world border, and replicated asynchronously through the backend-scoped same-identity view. Normal-mode Y, gravity and ground state are server-owned; client on-ground/wall flags are advisory. Creative/Spectator flight accepts bounded Y only after server-authorized mode state. Initial teleport confirmation and later corrections use separate readiness semantics.
- Use Item applies validated finite rotation to the local pose before acknowledging the read-only action; it still has no item effect.
- Creative inventory handling decodes the official Slot envelope and submits bounded data to the backend; authorization and authoritative state live in reducers/snapshots. Full vanilla item-component coverage and all container/crafting semantics remain incomplete.
- Settings, input flags, and sprint intent are connection-local. Accepted Survival sprint intent drains one food every five seconds and submits vitals through the backend state reducer. Health, food and death persist per offline profile in SpacetimeDB under a single active-session owner; disconnect/expiry releases the owner, and missing required subscribed state fails closed. Selected hotbar state is reducer-backed when M3 is available.

## Batch 1 — protocol hardening, controls, and world connection

Goal: preserve reliable session lifecycle while deciding explicit policies for optional cookies, transfers, Ping, movement authority, and reconfiguration. Framing/bounds, loading fallback, teleport acknowledgements/corrections, and local rotation handling are implemented; client-visible fallback confirmation remains outstanding.

| Dir | ID | Packet | Status | Current behavior / next work |
| --- | --- | --- | :---: | --- |
| CB | 0x15 | Cookie Request | - | Not sent; cookie storage/exchange is disabled. |
| CB | 0x18 | Plugin Message (clientbound) | - | No Play plugin messages are sent; Configuration brand is the only server plugin payload. |
| CB | 0x20 | Disconnect | I | Writes network-NBT reason and closes Play session. |
| CB | 0x2D | Keep Alive (clientbound) | I | Periodic challenge with timeout and RTT tracking. |
| CB | 0x32 | Login (play) | P | Pinned static world fields with confirmed owned backend entity ID; no process-local allocator. General world/game-mode fields remain static. |
| CB | 0x3E | Ping | - | No correlated Play ping is sent; Status Ping is a separate state. |
| CB | 0x3F | Ping Response | - | No Play Ping request is accepted, so no response is emitted. |
| CB | 0x48 | Look At | - | Add authoritative facing and entity-target fallback. |
| CB | 0x49 | Synchronize Player Position | P | Initial and repeated absolute corrections; general pose encoder matches official fixtures. One outstanding positive ID, exact acknowledgement and ten-second deadline; no physics authority. |
| CB | 0x4A | Player Rotation | - | Add server-directed absolute/relative rotation. |
| CB | 0x78 | Start Configuration | - | Not sent; there is no active Play → Configuration transition. |
| CB | 0x7A | Store Cookie | - | Not sent; cookie storage/exchange is disabled. |
| CB | 0x84 | Transfer | - | Not sent; transfer handshakes are rejected during Login. |
| SB | 0x00 | Confirm Teleportation | I | Initial/corrective IDs and echoed position/rotation matched bit-exactly; mismatched/replayed acknowledgement disconnects. Initial readiness remains independent of corrections. |
| SB | 0x0D | Client Tick End | P | Empty packet accepted/discarded; server loading timer advances independently of client traffic. |
| SB | 0x0E | Client Information | P | Settings stored; enforce view/chat/listing/skin preferences where relevant. |
| SB | 0x10 | Acknowledge Configuration | - | Unsupported: the server never sends Start Configuration; the unsolicited acknowledgement disconnects. |
| SB | 0x15 | Cookie Response | - | Unsupported: the server never sends a cookie request. |
| SB | 0x16 | Plugin Message (serverbound) | P | Brand decoded/stored; other channels are bounded then ignored. No other channels are enabled. |
| SB | 0x1C | Keep Alive (serverbound) | I | Matches outstanding challenge, records RTT, rejects wrong response. |
| SB | 0x1E | Set Player Position | P | Finite local movement and retained flags; out-of-static-box positions corrected. No physics or replication authority. |
| SB | 0x1F | Set Player Position and Rotation | P | Finite local pose and retained flags; out-of-static-box positions corrected. No physics or replication authority. |
| SB | 0x20 | Set Player Rotation | P | Finite local rotation and retained movement flags; replication remains pending. |
| SB | 0x21 | Set Player Movement Flags | P | Defined bits validated and retained as inputs without trusting client physics. |
| SB | 0x26 | Ping Request | - | Unsupported until a correlated Play Ping flow is separately implemented. |
| SB | 0x2A | Player Command | P | Only sprint start/stop stored; bed, horse, vehicle, elytra actions remain. |
| SB | 0x2B | Player Input | P | Validated flags stored locally; connect to authoritative controls/sneaking. |
| SB | 0x2C | Player Loaded | P | Explicit flag or nominal 60-server-tick fallback, together with initial teleport acknowledgement, gates backend-confirmed Play. Graphical smoke pending. |
| SB | 0x2D | Pong | - | Unsupported: no Play Ping is sent, so no Pong can be correlated. |

Remaining checks: client-visible loading without Player Loaded after the 60-tick fallback, teleport replay/order, and state transitions. Structural payload bounds, out-of-box corrective teleport, and item-use rotation are implemented; retain cryptographic verification as a later dependency.

## Batch 2 — inventory, containers, and crafting

Goal: extend the implemented M3-backed player inventory and simple storage flow into broader vanilla Slot/components, Hashed Slot, container state IDs, resynchronization, containers, and crafting. Reducers own mutations and snapshots remain authoritative; trades and specialized menus can follow.

| Dir | ID | Packet | Status | Current behavior / next work |
| --- | --- | --- | :---: | --- |
| CB | 0x11 | Close Container | I | Closes supported storage menu when authoritative snapshot returns to player inventory; extend reconciliation to all menu types. |
| CB | 0x12 | Set Container Content | I | Authoritative player/storage snapshot with revision; extend state-ID semantics to all vanilla menus. |
| CB | 0x13 | Set Container Property | - | Menu-specific progress/property updates. |
| CB | 0x14 | Set Container Slot | - | Authoritative delta with state ID and slot mapping. |
| CB | 0x16 | Set Cooldown | - | Authoritative cooldown groups and expiry. |
| CB | 0x2A | Open Horse Screen | - | Specialized menu with valid tracked mount entity. |
| CB | 0x35 | Merchant Offers | - | Authoritative offers, pricing, usage, and stock. |
| CB | 0x3B | Open Book | - | Open validated held book. |
| CB | 0x3C | Open Screen | I | Opens the supported simple-storage menu with authoritative ID/title; other screen types remain. |
| CB | 0x40 | Place Ghost Recipe | - | Recipe display for unavailable crafting placement. |
| CB | 0x4B | Recipe Book Add | - | Recipe displays, IDs, unlock/highlight state. |
| CB | 0x4C | Recipe Book Remove | - | Remove recipes consistently with unlock state. |
| CB | 0x4D | Recipe Book Settings | - | Synchronize per-book preferences. |
| CB | 0x62 | Set Cursor Item | I | Cursor stack from authoritative inventory snapshot. |
| CB | 0x6B | Set Held Item (clientbound) | I | Selected slot from authoritative inventory snapshot, range 0–8. |
| CB | 0x6E | Set Player Inventory Slot | - | Direct player slot mapping; avoid state-ID races. |
| CB | 0x88 | Update Recipes | - | Property sets and stonecutter recipe displays. |
| SB | 0x03 | Bundle Item Selected | - | Validate bundle selection against owned stack. |
| SB | 0x11 | Click Container Button | - | Validate menu/button and apply authoritative operation. |
| SB | 0x12 | Click Container | P | Submit bounded Hashed Slot evidence to backend; extend all vanilla click/drag modes and menu layouts. |
| SB | 0x13 | Close Container | P | Supported menu closure is reducer-backed; broaden window lifecycle and reconciliation to all vanilla menu types. |
| SB | 0x14 | Change Container Slot State | - | Validate Crafter menu and enabled-slot state. |
| SB | 0x18 | Edit Book | - | Validate owned book, pages/title bounds, and signing. |
| SB | 0x24 | Pick Item From Block | P | Position/data flag decoded/discarded; add permitted item selection. |
| SB | 0x25 | Pick Item From Entity | P | Entity/data flag decoded/discarded; add permitted item selection. |
| SB | 0x27 | Place Recipe | - | Validate recipe/menu and consume available ingredients. |
| SB | 0x2F | Change Recipe Book Settings | P | Packet is bounded/decoded; store and synchronize recipe-book preferences. |
| SB | 0x30 | Set Seen Recipe | - | Validate known recipe and persist seen state. |
| SB | 0x31 | Rename Item | - | Validate active anvil, name policy, costs, and output. |
| SB | 0x34 | Select Trade | - | Validate active merchant and selected offer. |
| SB | 0x35 | Set Beacon Effect | - | Validate menu, effect availability, and payment. |
| SB | 0x36 | Set Held Item (serverbound) | I | Range-checked selection updates the authoritative inventory when the supported inventory snapshot is active; otherwise it remains local baseline state. |
| SB | 0x39 | Set Creative Mode Slot | P | Slot and schema-driven component data are decoded and submitted to the backend; the narrow item allowlist, explicit permission and resynchronization policy leave general Creative behavior partial. |

Exit checks: no duplication through stale state IDs, cursor/drop/shift-click/drag operations, invalid component IDs/counts, unauthorized Creative writes, and reconnect persistence. Never treat client change lists or hashes as authoritative item creation.

Codec follow-up: the 122 component IDs/names now compare cleanly between the generated protocol enum and registry manifest. Hashed Slot decoding charges entries to the same aggregate element budget as nested ordinary Slots; focused tests cover empty/default patch comparison, malformed patch IDs/duplicates/truncation/counts, aggregate budget exhaustion, and nested NBT malformed lengths/tags/names. The inventory-core suite passes 15 tests. This bounds the wire reader but does not independently establish all 122 component field semantics or gameplay effects.

## Batch 3 — multiplayer entities and combat

Goal: subscription-driven entity visibility and lifecycle, tab-list/profile initialization before player spawn, movement/metadata/equipment replication, then authoritative interactions and damage. Implement current 26.3 stepped movement and LpVec3 layouts, not older movement codecs. Vehicles/projectiles may be later sub-batches.

Implementation record (2026-10-04): `assets/entity-26.3.json` contains official Java 26.3 codec samples for profile entry, player spawn, relative movement/rotation, profile removal and entity removal. Rust bounded encoders match the samples. Gateway workers emit profile-before-spawn bundles, movement, overflow respawn fallback and removal from a sender-scoped backend view. This establishes registered identity scope, not sharing across gateway processes. Opt-in disposable live coverage is added but has not been run, and two-client graphical/backpressure acceptance remains open.

| Dir | ID | Packet | Status | Current behavior / next work |
| --- | --- | --- | :---: | --- |
| CB | 0x00 | Bundle Delimiter | I | Bounded profile-before-player-spawn bundle with explicit start/end delimiters. |
| CB | 0x01 | Spawn Entity | I | Emits visible backend-owned players with stable entity/UUID/type and initialized profile/list entry. Other entity types remain unsupported. |
| CB | 0x02 | Entity Animation | - | Broadcast supported entity animations. |
| CB | 0x19 | Damage Event | - | Valid damage registry references and source attribution. |
| CB | 0x22 | Entity Event | - | Entity-type-valid status events. |
| CB | 0x23 | Teleport Entity | - | Absolute linear/stepped entity position synchronization. |
| CB | 0x2B | Hurt Animation | - | Damage-direction animation for tracked entities. |
| CB | 0x36 | Update Entity Position | I | Fixture-backed 26.3 fixed-point deltas and on-ground state; bounded subscription delivery. |
| CB | 0x37 | Update Entity Position and Rotation | I | Fixture-backed position/rotation updates; out-of-range deltas remove and respawn from the latest absolute backend pose. |
| CB | 0x38 | Move Minecart Along Track | - | Optional track interpolation and movement steps. |
| CB | 0x39 | Update Entity Rotation | I | Fixture-backed rotation/on-ground updates for visible players. |
| CB | 0x3A | Move Vehicle (clientbound) | - | Correct client-controlled vehicle movement. |
| CB | 0x43 | End Combat | - | Optional combat lifecycle notification; vanilla client unused. |
| CB | 0x44 | Enter Combat | - | Optional combat lifecycle notification; vanilla client unused. |
| CB | 0x46 | Player Info Remove | I | Remove offline profile/list entries after entity despawn on session removal. |
| CB | 0x47 | Player Info Update | I | Initialize bounded offline profile/list entry before spawn; no unverified signed-chat claims. |
| CB | 0x4E | Remove Entities | I | Despawn visible players when the backend view removes their session. |
| CB | 0x55 | Set Head Rotation | - | Synchronize head/body facing appropriately. |
| CB | 0x65 | Set Entity Metadata | - | Typed entity metadata, flags, poses, and defaults. |
| CB | 0x66 | Link Entities | - | Validate leash attachment and detach lifecycle. |
| CB | 0x67 | Set Entity Velocity | - | Current LpVec3 velocity encoding and knockback. |
| CB | 0x68 | Set Equipment | - | Equipment Slot codec and update continuation bits. |
| CB | 0x6D | Set Passengers | - | Authoritative mount/passenger graph. |
| CB | 0x7B | Swing Animation | - | Hand/type/duration broadcast, including punch feedback. |
| CB | 0x7F | Pickup Item | - | Pickup animation paired with inventory mutation and entity removal. |
| CB | 0x80 | Synchronize Vehicle Position | - | Relative/absolute vehicle correction, distinct from Teleport Entity. |
| CB | 0x8A | Projectile Power | - | Optional projectile acceleration/power synchronization. |
| SB | 0x01 | Attack | P | Entity ID decoded/discarded; validate target, reach, visibility, cooldown, damage. |
| SB | 0x1A | Interact | - | Current hand/LpVec3 offset/sneak fields and authoritative target action. |
| SB | 0x22 | Move Vehicle (serverbound) | - | Validate ownership, finite movement, collision, and correction. |
| SB | 0x23 | Paddle Boat | - | Validate rider and update boat metadata. |
| SB | 0x2E | Punch | P | Empty packet accepted/discarded; add authoritative swing/action feedback. |

Exit checks: two clients see consistent spawn/move/remove/equipment state; one client's disconnect removes entities from peers; invalid or hidden entity targets cannot damage or interact. Subscription reads expose server state; reducers mutate it transactionally and authorize from the sender, not packet-provided identity. Movement inputs must be coalesced with bounded per-session and per-recipient queues; visibility comes only from gateway-sender-scoped backend rows/views (or a strictly local same-process projection whose limitation is explicit), never a global public session subscription.

## Batch 4 — chat and commands

Goal: explicit unsigned/system-chat policy first, command parsing/permissions/completion next, and signed-chat support only when session certificates, signatures, timestamps, chain indices, last-seen acknowledgements, and checksums are actually verified. Structural parsing alone is not cryptographic verification.

| Dir | ID | Packet | Status | Current behavior / next work |
| --- | --- | --- | :---: | --- |
| CB | 0x0F | Command Suggestions Response | - | Correlated transaction/range and permission-aware suggestions. |
| CB | 0x10 | Commands | - | Valid command graph/parser IDs matching executable commands. |
| CB | 0x17 | Chat Suggestions | - | Optional managed chat completions. |
| CB | 0x1F | Delete Message | - | Signed-message cache lookup/removal; not for system messages. |
| CB | 0x21 | Disguised Chat Message | - | Unsigned command/console messages with chat-type formatting. |
| CB | 0x42 | Player Chat Message | - | Routed player chat with correct signing/filter/session policy. |
| CB | 0x7C | System Chat Message | I | Operator broadcasts, notices and explicitly unsigned player messages as network NBT, overlay=false; never signed Player Chat. |
| SB | 0x06 | Acknowledge Message | P | Nonnegative count parsed/discarded; maintain last-seen acknowledgement state. |
| SB | 0x07 | Chat Command | I | Implements the bounded `/help`, `/storage <UUID>` and `/inventory` allowlist; no command graph/suggestions or admin/mode commands. |
| SB | 0x08 | Signed Chat Command | P | Structural fields consumed/discarded with notice; no signature validation/execution. |
| SB | 0x09 | Chat Message | P | Bounded unsigned messages route through owned-session/rate/mode checks and gateway-scoped retained history; signed payloads are refused, never verified. |
| SB | 0x0A | Player Session | P | Public key (≤512 bytes) and signature (≤4096 bytes) are structurally bounded then discarded; add verified session lifecycle. |
| SB | 0x0F | Command Suggestions Request | - | Bounded text, request correlation, and permission-aware completion. |

Source implements UTF-16/string bounds, rate limits, hidden/commands-only settings, scoped retained history, and truthful unsigned System Chat delivery. Exit checks still include disposable-backend reducer/view execution, two-client graphical exchange, queue overflow/reconnect behavior and unauthorized command cases. Malformed/expired keys, replayed signatures, chain/checksum failures and cryptographic chat/session verification are outside this implementation; never relabel signed payloads as verified or authenticated player messages.

Local worker regression fills the recipient chat queue and confirms overflow closes the Play session. This covers the fail-closed worker response, not live backend history pruning, two-client slow-recipient behavior, or reconnect delivery; those remain open.

## Batch 5 — world streaming and block interactions

Goal: replace the fixed patch with authoritative world storage/subscriptions and per-client chunk interest, batch flow control, unloads, block/light updates, and validated interaction mutation. Corrections must precede sequence acknowledgements where prediction needs rollback. Presentation effects depend on real world actions, not fabricated success.

| Dir | ID | Packet | Status | Current behavior / next work |
| --- | --- | --- | :---: | --- |
| CB | 0x04 | Acknowledge Block Change | I | Writes action/use sequence ACK after the supported action result is confirmed or corrected; backend loss disconnects the session. |
| CB | 0x05 | Set Block Destroy Stage | - | Track mining progress and clear cancelled/completed stages. |
| CB | 0x06 | Block Entity Data | - | Registry-correct NBT snapshots/deltas for block entities. |
| CB | 0x07 | Block Action | - | Authoritative block animations/events. |
| CB | 0x08 | Block Update | P | Static correction plus supported committed override updates on loaded chunks; generalized chunk encoding and live peer visibility remain open. |
| CB | 0x0B | Chunk Batch Finished | P | Official-codec batch count for initial and streaming windows; client feedback/pacing adaptation remains. |
| CB | 0x0C | Chunk Batch Start | P | Opens bounded recenter batches; per-tick byte budgeting and client feedback adaptation remain. |
| CB | 0x0D | Chunk Biomes | - | Dynamic biome section updates. |
| CB | 0x24 | Explosion | - | Authoritative explosion and current effects/velocity layout. |
| CB | 0x25 | Add Transient Block | - | Optional finite-lived transient block presentation. |
| CB | 0x26 | Unload Chunk | I | Official-codec fixture-backed unload as client-centered chunk windows move. |
| CB | 0x2C | Initialize World Border | - | Initial authoritative border state. |
| CB | 0x2E | Chunk Data and Update Light | P | Official-codec-derived flat chunks stream in a capped moving window; per-chunk overrides reapply on load. General palettes and cross-gateway interest remain. |
| CB | 0x2F | World Event | - | World action sound/particle events. |
| CB | 0x31 | Update Light | - | Incremental validated light masks/arrays. |
| CB | 0x3D | Open Sign Editor | - | Authorize editor only for a valid sign and text slot. |
| CB | 0x56 | Update Section Blocks | - | Batch authoritative changes by section/tick. |
| CB | 0x5A | Set Border Center | - | Border center updates. |
| CB | 0x5B | Set Border Lerp Size | - | Border transition with real-time duration semantics. |
| CB | 0x5C | Set Border Size | - | Immediate border size update. |
| CB | 0x5D | Set Border Warning Delay | - | Border warning timing. |
| CB | 0x5E | Set Border Warning Distance | - | Border warning distance. |
| CB | 0x60 | Set Center Chunk | I | Initialize and recenter the bounded chunk window from accepted player movement. |
| CB | 0x61 | Set Render Distance | I | Clamp client cache distance to the supported radius cap of two chunks. |
| CB | 0x71 | Set Simulation Distance | I | Synchronize the current capped simulation distance (two chunks maximum). |
| CB | 0x73 | Update Time | - | Current world-clock array layout, not legacy two-long time. |
| CB | 0x89 | Update Tags | - | Play-time tag updates consistent with registry IDs. |
| SB | 0x0B | Chunk Batch Received | P | Positive finite rate validated/discarded; add pacing and outstanding-batch limits. |
| SB | 0x29 | Player Action | P | Every player is enabled under normal mode rules: supported stone/grass Survival breaks award stone/dirt; Creative breaks do not award drops; Adventure requires supported `CanDestroy` permission components and currently fails closed; Spectator never mutates. Unsupported statuses correct/ACK without mutation. |
| SB | 0x3E | Update Sign | - | Require authorized editor, valid block/text slot, bounded lines. |
| SB | 0x42 | Use Item On | P | Every player is enabled under normal mode rules: supported Survival placement commits terrain and consumes stone; Creative does not consume; Adventure requires supported `CanPlaceOn` permission components and currently fails closed; Spectator never mutates. Other targets correct before ACK; containers remain unsupported. |
| SB | 0x43 | Use Item | P | Validated finite rotation applied locally before read-only sequence ACK; no item-use effect. |

Exit checks: crossing chunk borders, negative coordinates, changes visible to multiple clients, unload/reload, batch feedback/backpressure, heightmaps/palettes/light masks, block entity consistency, reach/permission/sequence checks, and persistence. Reconcile held-item rotation before executing use; inventory-dependent action variants require Batch 2. User-selected edits are enabled for every logged-in player under normal mode permissions, not unrestricted in Adventure/Spectator.

Local regression coverage (fake backend): a block action remains pending until reducer success and matching subscribed action state; overlapping predictions are corrected and ACKed without a second reducer call; rejected predictions receive the authoritative block correction before ACK; deadlines close the session; and a delayed result from a disconnected session cannot affect its replacement. Chunk load covers changes between snapshot preflight/apply, a newer revision reconciled on the next poll after batch completion, and unloading then reloading a chunk whose override changed while absent. The ignored disposable live test checks exact Survival retry idempotency, races two authorized sessions against one block, forces the chunk override cap after staging an inventory drop to assert rollback across inventory/action/chunk views, reconnects the same gateway identity and verifies vitals persist in a new profile session, then restores chunk interest and checks that the new connection receives the committed world override snapshot. It compiles but has not been run. Backend-process restart and graphical persistence remain unverified.

## Batch 6 — player state, survival, and respawn

Goal: authoritative health/food/experience/attributes/effects, game-mode abilities, statistics, death and respawn/dimension lifecycle. Keep state synchronization distinct from static Creative initialization. Respawn depends on Batch 1 loading/teleport control and Batch 5 chunk delivery.

| Dir | ID | Packet | Status | Current behavior / next work |
| --- | --- | --- | :---: | --- |
| CB | 0x03 | Award Statistics | - | Respond to requests with tracked statistics/deltas. |
| CB | 0x0A | Change Difficulty | - | Synchronize difficulty/lock state. |
| CB | 0x27 | Game Event | P | Event 13 starts loading; event 3 synchronizes all four supported game modes. Other game events remain unsupported. |
| CB | 0x28 | Game Rule Values | - | Respond with authorized/current game rules. |
| CB | 0x41 | Player Abilities (clientbound) | P | Protocol 777 flags derive from Survival/Creative/Adventure/Spectator; flight remains bounded and follows server-authorized mode state. |
| CB | 0x45 | Combat Death | P | Void death emits the bounded death message for Survival/Adventure; other damage/death sources are unsupported. |
| CB | 0x4F | Remove Entity Effect | - | Remove expired/cleared effects. |
| CB | 0x54 | Respawn | P | Explicit Survival/Adventure void respawn waits for reducer success plus matching subscribed persistent state, uses official mode-specific same-dimension fixtures, restores health, preserves inventory/food, refreshes bounded chunks and teleports to spawn; live/graphical acceptance and other respawn causes remain open. |
| CB | 0x63 | Set Default Spawn Position | P | Advertises the static initial spawn; void respawn uses official mode-specific CommonPlayerSpawnInfo and a corrective teleport. Configurable spawn points remain unsupported. |
| CB | 0x69 | Set Experience | - | Synchronize authoritative XP/level/progress. |
| CB | 0x6A | Set Health | P | Synchronizes persistent health/food/death, accepted sprint loss, void death and respawn; live reducer confirmation, other damage sources and saturation simulation remain. |
| CB | 0x86 | Update Attributes | - | Registry-correct attributes and modifiers. |
| CB | 0x87 | Entity Effect | - | Authoritative effects, durations/amplifiers/flags. |
| SB | 0x0C | Client Status | P | `PERFORM_RESPAWN` is accepted only while dead; statistics and game-rule requests remain unsupported. |
| SB | 0x28 | Player Abilities (serverbound) | P | Bounded flying request accepted only in Creative/Spectator; client request never grants a mode or permission. |

Exit checks: live reconnect/restart persistence, death/respawn without explicit loaded packet, effect expiry, hunger/XP changes, attribute modifiers, and dimension changes. Client movement flags and ability requests are inputs, not proof of server-authorized flight or survival outcomes.

Local fake-backend regressions verify respawn waits for both reducer success and matching subscribed vitals; stale subscription data does not advance the client. They also verify respawn clears pending block/inventory work and stale inventory snapshots, resets loading readiness, queues bounded chunk reload, and rejects movement until the corrective teleport ACK arrives. Live reducer/reconnect/restart acceptance still requires the opt-in disposable-backend test; graphical death/respawn remains open.

## Batch 7 — presentation, resources, and advanced/optional features

Goal: UI/audio/resources and feature-specific integrations after authoritative core state. These remain individually optional; deliberate non-support should have an explicit policy. Resource downloads expose client network/bandwidth implications; links/dialog actions need validation. Spectator features require entity visibility and permissions.

| Dir | ID | Packet | Status | Current behavior / next work |
| --- | --- | --- | :---: | --- |
| CB | 0x09 | Boss Bar | - | Optional managed bar lifecycle/style/progress. |
| CB | 0x0E | Clear Titles | - | Optional title clear/reset. |
| CB | 0x30 | Particle | - | Current particle/randomization fields and registry data. |
| CB | 0x34 | Map Data | - | Optional map pixels/icons and persistent map IDs. |
| CB | 0x50 | Reset Score | - | Remove scoreboard entries/objective associations. |
| CB | 0x51 | Remove Resource Pack | - | Optional UUID-scoped/all pack removal. |
| CB | 0x52 | Add Resource Pack | - | Optional pack URL/hash/prompt/forced policy. |
| CB | 0x53 | Post Effects | - | Optional post-processing effect selection. |
| CB | 0x57 | Select Advancements Tab | - | Optional advancement tab synchronization. |
| CB | 0x58 | Server Data | - | Play MOTD/icon update, distinct from Status response. |
| CB | 0x59 | Set Action Bar Text | - | Optional action-bar text components. |
| CB | 0x5F | Set Camera | - | Spectator camera lifecycle and reset on invalid target. |
| CB | 0x64 | Display Objective | - | Select scoreboard display slot/objective. |
| CB | 0x6C | Update Objectives | - | Objective create/update/remove and number formats. |
| CB | 0x6F | Update Teams | - | Teams, names, visibility/collision/friendly-fire policy. |
| CB | 0x70 | Update Score | - | Score values/display/number format. |
| CB | 0x72 | Set Subtitle Text | - | Optional subtitle presentation. |
| CB | 0x74 | Set Title Text | - | Optional title presentation. |
| CB | 0x75 | Set Title Animation Times | - | Optional title timing. |
| CB | 0x76 | Entity Sound Effect | - | Registry/inline entity sound events. |
| CB | 0x77 | Sound Effect | - | Positioned sound events and categories. |
| CB | 0x79 | Stop Sound | - | Optional source/name-filtered sound stop. |
| CB | 0x7D | Set Tab List Header And Footer | - | Optional server tab-list text. |
| CB | 0x85 | Update Advancements | - | Optional advancement definitions/progress/rewards integration. |
| CB | 0x8C | Server Links | - | Optional validated menu links/labels. |
| CB | 0x8D | Waypoint | - | Optional locator entries with visibility/privacy policy. |
| CB | 0x8E | Clear Dialog | - | Optional dialog lifecycle. |
| CB | 0x8F | Show Dialog (play) | - | Optional registry/inline NBT dialog definition. |
| SB | 0x32 | Resource Pack Response | - | Correlate UUID/result and enforce advertised pack policy. |
| SB | 0x33 | Seen Advancements | - | Track valid tab/screen state. |
| SB | 0x3F | Spectator Action | - | Validate mode/target and update camera. |
| SB | 0x40 | Teleport To Entity | - | Validate spectator permission/UUID; dimension lifecycle if needed. |
| SB | 0x44 | Custom Click Action | - | Optional identifier/payload dispatch; bounded NBT, explicit allowlist. |

Exit checks: valid text/NBT/registry references, lifecycle removal/reset, pack-result correlation, privacy and permissions, and safe non-support for deferred features. Teams can affect gameplay, so their authoritative policy must not be only cosmetic.

## Batch 8 — administration, debugging, and testing (optional)

Goal: explicitly permission-gated operator tools, debug subscriptions, game tests, and server controls. Do not expose NBT, arbitrary command execution, or structure mutation to ordinary clients. Debug telemetry should be bounded and subscriptions should be cleaned up with the session.

| Dir | ID | Packet | Status | Current behavior / next work |
| --- | --- | --- | :---: | --- |
| CB | 0x1A | Debug Block Value | - | Optional subscribed block debug updates. |
| CB | 0x1B | Debug Chunk Value | - | Optional subscribed chunk debug updates. |
| CB | 0x1C | Debug Entity Value | - | Optional subscribed entity debug updates. |
| CB | 0x1D | Debug Event | - | Optional typed debug events. |
| CB | 0x1E | Debug Sample | - | Optional authorized tick metrics. |
| CB | 0x29 | Game Test Highlight Position | - | Optional test/debug highlight. |
| CB | 0x33 | Low Disk Space Warning | - | Optional actual storage warning, not fabricated telemetry. |
| CB | 0x7E | Tag Query Response | - | Permission-checked NBT query response with transaction ID. |
| CB | 0x81 | Test Instance Block Status | - | Optional test screen status/size updates. |
| CB | 0x82 | Set Ticking State | - | Optional authoritative tick rate/freeze controls. |
| CB | 0x83 | Step Tick | - | Optional frozen simulation stepping. |
| CB | 0x8B | Custom Report Details | - | Optional bounded diagnostic context without secrets. |
| SB | 0x02 | Query Block Entity Tag | - | Authorized block NBT lookup and transaction correlation. |
| SB | 0x04 | Change Difficulty | - | Permission-gated difficulty mutation. |
| SB | 0x05 | Change Game Mode | P | Client requests may select Survival/Adventure through the owned-session reducer. Creative/Spectator changes are denied here; administrators use the profile-scoped grant reducer. |
| SB | 0x17 | Debug Subscription Request | - | Replace requested subscription set; gate emitted data by permission. |
| SB | 0x19 | Query Entity Tag | - | Authorized visible/entity NBT lookup. |
| SB | 0x1B | Jigsaw Generate | - | Optional permission-gated structure generation. |
| SB | 0x1D | Lock Difficulty | - | Permission-gated difficulty lock. |
| SB | 0x37 | Program Command Block | - | Optional authorized command-block editing/execution policy. |
| SB | 0x38 | Program Command Block Minecart | - | Optional authorized command-minecart editing. |
| SB | 0x3A | Set Game Rules | - | Permission-gated rule validation/mutation. |
| SB | 0x3B | Program Jigsaw Block | - | Optional validated structure-pool metadata editing. |
| SB | 0x3C | Program Structure Block | - | Optional bounded/authorized structure operations. |
| SB | 0x3D | Set Test Block | - | Optional authorized test-block editing. |
| SB | 0x41 | Test Instance Block Action | - | Optional authorized test-instance operations. |

Exit checks: no ordinary-user privilege escalation, bounded NBT/structures/subscriptions, denied data remains private, and tick controls match actual simulation behavior. External filesystem/network work must not run in SpacetimeDB reducers; keep reducer time/randomness deterministic and use context-provided values.

## Verification and coverage audit

Independently count **table rows**, not the page's contents headings: shared-state packets are omitted from the dedicated Play subsection heading list but are present in the authoritative Play inventory tables.

- Fresh source CB table: 144 rows, unique decimal IDs **0–143**, unique hex IDs **0x00–0x8F**, contiguous with no gaps.
- Fresh source SB table: 69 rows, unique decimal IDs **0–68**, unique hex IDs **0x00–0x44**, contiguous with no gaps.
- This roadmap's eight functional inventory tables: **213 rows**, exactly one row for every source `(direction, ID)`, with packet names matching the current inventory tables.
- Baseline decode count expands the movement range into four IDs and counts the two command IDs separately: **31 SB**, not enum-variant count. The current decoder handles 31 distinct IDs. CB emits **30** distinct IDs: twenty-one dynamic packet IDs plus nine pinned login/initialization IDs (chunk frames share one ID).
- I/P totals measure current-purpose completion vs partial handling, not protocol completeness: **25 I + 41 P + 147 - = 213**. Emitted/accepted totals: **30/144 CB** and **31/69 SB**. No discard-only input is I.

| Batch | CB | SB | Total | I | P | - |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 13 | 16 | 29 | 4 | 12 | 13 |
| 2 | 17 | 16 | 33 | 6 | 6 | 21 |
| 3 | 27 | 5 | 32 | 8 | 2 | 22 |
| 4 | 7 | 6 | 13 | 2 | 4 | 7 |
| 5 | 27 | 5 | 32 | 5 | 8 | 19 |
| 6 | 13 | 2 | 15 | 0 | 8 | 7 |
| 7 | 28 | 5 | 33 | 0 | 0 | 33 |
| 8 | 12 | 14 | 26 | 0 | 1 | 25 |
| Total | 144 | 69 | 213 | 25 | 41 | 147 |

The inventory rows are the source of truth for status; audit totals must be recomputed when handling changes. Verification should compare unique ID sets and packet names against the fetched source rather than relying solely on contiguous ranges or an earlier audit.

## Recommended execution order

1. **Batch 1 finish:** inspect a graphical client through the Player Loaded and 60-tick fallback paths; extend movement from local pose to authoritative physics/replication when the world model exists. Loading, structural bounds, corrective teleports, and item-use rotation handling are implemented.
2. **Batch 2 extend:** broaden official Slot/component coverage and Hashed Slot validation, then add vanilla menu/state-ID semantics, containers, crafting, and specialized menus. Player inventory, selected slot/cursor snapshots, permission-checked Creative writes, simple storage, and backend-authoritative reducer updates already exist.
3. **Batch 3 multiplayer:** registered gateway-view visibility, bundled profile/spawn, movement, overflow fallback and removal are implemented in source. Validate through the opt-in disposable live test and two graphical clients; cross-process/world sharing, metadata/equipment and interactions/combat remain future work.
4. **Batch 4 extend:** general command parsing/permissions, command graph and suggestions; retain opt-in live and graphical gates for M5 unsigned routing. `/help`, `/storage`, `/inventory`, bounded backend-routed unsigned player chat, scoped history and hidden/commands-only policy are implemented in source; signed chat remains refused.
5. **Batch 5 world streaming/interactions:** finish disposable-backend mutation/rollback/concurrency checks and two-client graphical edit visibility across unload/reload, reconnect and gateway/backend restart. Source has bounded flat-world streaming, sender-scoped persistent overrides/snapshots, authorization, atomic supported inventory deltas, commit-confirmed ACKs and peer updates. Survival/Creative supported operations work in source; Adventure remains fail-closed until component permissions are supported, and Spectator never mutates.
6. **Batch 6 survival/respawn:** source has four modes, abilities and persistent health/food/death synchronization with active-session ownership, accepted-sprint food drain, bounded gravity/platform landing, border clamping and same-dimension void death/respawn. Validate death and respawn with the graphical client and confirm mode/vital behavior under backend/session loss. Disposable-backend reconnect/restart behavior is not yet verified.
7. **Batches 7–8:** select optional features intentionally; do not make exhaustive inventory imply every optional feature must ship.

For each implementation increment: update its single owning row, add exact current-protocol codec samples and malformed-boundary cases, verify server policy and client-visible behavior, and recalculate direction/status counts. Current unit and module checks establish local behavior only; disposable backend and graphical-client gates remain explicitly listed above.
