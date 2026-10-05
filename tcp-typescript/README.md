# tcp_typescript

To install dependencies:

```bash
bun install
```

To run:

```bash
bun run index.ts
```

This project was created using `bun init` in bun v1.4.2. [Bun](https://bun.com) is a fast all-in-one JavaScript runtime.

## Live worlds UI

The browser UI now presents the live TCP data as a full-screen Three.js factory floor. Endpoints
become stations and warehouses, connections become illuminated logistics routes, and packets become
cargo moving along the floor. The surrounding HUD shows operational status, throughput, recent
packet cargo, active routes, and the live data source.

The UI subscribes to the generated SpacetimeDB client bindings when these Vite variables are set:

```powershell
$env:VITE_SPACETIMEDB_URI = "ws://127.0.0.1:3000"
$env:VITE_SPACETIMEDB_DB = "tcp-capture"
bun run dev
```

For a local database, use separate terminals:

```powershell
bun run spacetime:start
bun run spacetime:publish
bun run dev
```

The first command stays running. The second publishes the module to the local server. If the
server is not running, the browser will report `WebSocket error` and intentionally use the capture
bridge/demo fallback.

The SpacetimeDB module source remains [`packetCapture_SpacetimeDB.ts`](./packetCapture_SpacetimeDB.ts).
The small [`spacetime/src/index.ts`](./spacetime/src/index.ts) file is only the CLI entrypoint used
to generate [`frontend/module_bindings`](./frontend/module_bindings); the browser cannot import the
server-only `spacetimedb/server` module directly. If the database is unavailable, the UI falls back
to the pktmon bridge and then to a local demo pulse stream.

## React Flow live capture

The React Flow UI connects to a Bun WebSocket bridge on port `8787`. On Windows, start the
capture bridge from an elevated PowerShell because `pktmon` requires access to its driver:

```powershell
cd C:\Users\nerfs\Documents\Projects\Protocols\TCP\Typescript\TCP_Typescript
bun run capture
```

If the UI reports `pktmon exited with code 159`, the capture terminal was not elevated. Restart
PowerShell with `Run as administrator`, then run `bun run capture` again. `pktmon status` should no
longer report `Access is denied`.

In a second terminal, start the UI:

```powershell
bun run dev
```

Open `http://localhost:5173`. The UI shows `REAL CAPTURE` only after the bridge reports that
`pktmon` is actively capturing. Otherwise it stays in its local demo mode. The bridge forwards
TCP metadata (endpoints, flags, sequence/acknowledgment numbers, window, and payload length)
and intentionally does not forward application payload contents.

Each observed source and destination is rendered as its own React Flow endpoint card. Packet
events create or update the directed edge between the two cards, highlight both endpoints, and
show the actual source-to-destination route on the edge label.

## SpacetimeDB capture model

[`packetCapture_SpacetimeDB.ts`](./packetCapture_SpacetimeDB.ts) is the SpacetimeDB v2 TypeScript
module for the same capture stream. It keeps three public tables:

- `endpoint`: one aggregate row per address and TCP port;
- `connection`: one aggregate row per directed TCP four-tuple;
- `packet`: the append-only event stream used for packet pulses and inspection.

The `ingestPacket` reducer validates packet metadata, updates endpoint and connection aggregates,
and inserts the packet event in one transaction. `clearCapture` removes the current capture state.
The reducer intentionally does not invoke `pktmon`, sockets, or other operating-system APIs:
capture remains the responsibility of the privileged Bun bridge, which should translate each
decoded TCP segment into an `ingestPacket` call. This keeps the database module deterministic and
makes it usable by both the live UI and later replay tools.

The module is typechecked with:

```powershell
bun run typecheck
```

Publishing it requires the SpacetimeDB CLI and a module/database target; that deployment wiring is
kept separate from the packet parser so local TCP tests continue to run with Bun.

## TCP core verification

The single-file implementation is [`index-2.ts`](./index-2.ts). Run its Bun test and benchmark
trace with:

```powershell
bun run test
bun run typecheck
```

It covers TCP header encoding/decoding, IPv4/IPv6 checksums, options, serial-number window
validation, connection state transitions, retransmission and SACK bookkeeping, congestion and
flow control, zero-window probing, timers, ICMP error notifications, and property-based tests.

The file implements the TCP transport core and its lower-layer adapter boundary. It does not
pretend to be a complete IP stack: IP header construction, TTL carriage, routing, fragmentation,
and native ICMP packet parsing remain responsibilities of the caller, which can pass decoded
wire segments and lower-layer error notifications into the exported APIs.

### RFC 9293 verification boundary

| Area | Current evidence | Status |
| --- | --- | --- |
| Header fields, flags, reserved bits, checksum | Property tests plus IPv4/IPv6 reference checksum tests | Covered |
| EOL/NOP/MSS/window-scale/SACK/timestamps | Option combination properties and handshake tests | Covered |
| Serial sequence/window validation | Wraparound and oversized-overlap properties | Covered |
| Connection demultiplexing | IPv4/IPv6 family plus local/remote address and port four-tuple routing | Covered |
| Open, simultaneous open, close, TIME-WAIT, RST | Deterministic state-machine integration tests | Covered |
| Retransmission, RTT/RTO, congestion, SACK, zero-window probing | Timer, flow-control, and retransmission tests | Covered |
| IP header/TTL/routing/fragmentation | Not implemented in this transport core | Lower-layer boundary |
| Native ICMP packet parsing | Decoded error notification API only | Lower-layer boundary |
| Full operating-system socket semantics | Not implemented | Out of scope |

The normative reference is [RFC 9293](https://www.rfc-editor.org/rfc/rfc9293.html); passing the
local test suite verifies the listed core behaviors, not the unimplemented IP stack boundary.
