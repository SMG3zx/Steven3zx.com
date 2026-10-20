# Control plane to Agent protocol strategies for Janus

Researched on 2026-10-20 for Linear ENG-78. Facts and sources only, no recommendation and no ranking. All claims come from primary sources (Tailscale docs and source, Headscale docs and source at tag v0.29.5, pkg.go.dev, RFCs at rfc-editor.org, grpc.io and github.com/grpc/grpc, docs.nats.io, OASIS MQTT specs). Each source was read through a fetch tool that summarises pages, so quotes are as reported by that tool. Anything not confirmed on a primary source is marked **unconfirmed**; measurements not found are marked **not found**. One source is outside the requested list: the WHATWG HTML Standard for Server-Sent Events (it is the only primary definition of SSE).

**Version baseline:** Headscale v0.29.5 (see `headscale-derp-hosting.md`). `tailscale.com/tsnet` and `tailscale.com/client/local` v1.104.1 (published Oct 7, 2026 per pkg.go.dev). `google.golang.org/grpc/keepalive` v1.84.0 (pkg.go.dev, not the latest). `nats.go` v1.54.0 (pkg.go.dev, not the latest). `golang.org/x/net/http2` v0.61.0. Go `net` docs at Go 1.27.3. RFC 9110, 9113, 9530, 6455, 1122. MQTT v3.1.1 OS and v5.0.

**Vocabulary** (from `janus/janus-go/GLOSSARY.md`, Deployments section, and ADR 0002): a Deployment is pinned to one Agent; Healthy means the Agent fetched the Artifact, verified its hash, compiled it, and one `GET /` probe ended without trap, timeout or out of fuel; "Agent offline" is not a state: the Deployment stays healthy and the edge answers 503; if the control plane restarts mid-Deploy it reconciles with the Agent. Per request the Agent starts the WASI module (up to 16 concurrent, 30 s timeout, 10 MiB bodies).

## 1. Shared mechanics: what the tailnet gives each strategy

These facts apply to more than one strategy and are stated once.

### 1.1 tsnet and Headscale

- tsnet is "a library that lets you embed Tailscale inside a Go program" with a userspace TCP/IP stack ([Tailscale tsnet KB](https://tailscale.com/kb/1244/tsnet)). The KB page does not mention a custom control server, tags or WhoIs (fetched 2026-10-20).
- `tsnet.Server` fields (pkg.go.dev v1.104.1, [tsnet](https://pkg.go.dev/tailscale.com/tsnet)): `Dir` "specifies the name of the directory to use for state"; `AuthKey` "is the auth key to create the node", takes precedence over `TS_AUTHKEY`, and "is ignored if the node is already enrolled in the stored state"; `Ephemeral` registers an ephemeral node; `AdvertiseTags` are tags "for purposes of ACL enforcement", and the control server may not allow the node to adopt them. `ControlURL` "optionally specifies the coordination server URL"; empty falls back to `TS_CONTROL_URL`, then the Tailscale default.
- Methods (same page): `Up` "connects the server to the tailnet and waits until it is running"; `Listen` "announces only on the Tailscale network"; `ListenTLS` wraps a TLS listener; `ListenFunnel` "announces on the public internet using Tailscale Funnel" (TCP 443, 8443, 10000); `Dial` "connects to the address on the tailnet"; `LocalClient` returns a client that speaks to the server's local API. `Listen`, `ListenTLS`, `Dial` and `LocalClient` start the server if needed.
- **tsnet against Headscale: unconfirmed as a documented combination.** pkg.go.dev's tsnet page does not mention Headscale. The Headscale clients and tools pages make no statement about tsnet ([clients](https://headscale.net/stable/about/clients/), [tools](https://headscale.net/stable/ref/integration/tools/), per `headscale-derp-hosting.md` section 3). What does exist:
  - `ControlURL` is a generic field, so pointing it at a Headscale `server_url` is how the field is documented to work (pkg.go.dev above). That Headscale accepts a tsnet-registered node is inferred, not stated.
  - Headscale's own repo embeds tsnet for a debug tool: [hscontrol/tailsql.go at v0.29.5](https://github.com/juanfont/headscale/blob/v0.29.5/hscontrol/tailsql.go) sets `Dir`, `Hostname` and `Logf` on a `tsnet.Server` and calls `Up`. It sets no `ControlURL` or `AuthKey` in the part read, so it does not show tsnet registering against Headscale.
  - Headscale issue [#3151](https://github.com/juanfont/headscale/issues/3151) (closed "not planned" 2026-09-13, stale) asks about a userspace tsnet/Caddy node on the same host as Headscale; it quotes the FAQ as saying that setup "might work, but it is not supported". Two commenters reported a full Tailscale node (not tsnet) and a CoreDNS pod working or partly working; nobody reported tsnet specifically.
  - The Headscale issue search for `tsnet` (16 hits on 2026-10-20, titles only read) shows no issue titled as a tsnet incompatibility. Titles only; bodies not read.
  - Headscale minimum client version for 0.29.x is Tailscale v1.80.0; tsnet at v1.104.1 is newer than that, but no page maps tsnet versions to Headscale versions ([CHANGELOG](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md), per the earlier note). Compatibility is inferred: **unconfirmed**.

### 1.2 Outbound-only reachability: what a tsnet node can and cannot do

- Firewall: Tailscale says "Most of the time, you don't need to open any firewall ports". The listed rules are connections the device starts: TCP 443, UDP from 41641, UDP 3478, optional TCP 80 ([firewall ports KB](https://tailscale.com/kb/1082/firewall-ports)).
- Direct vs relayed: "Direct connections usually provide the lowest latency and highest throughput"; "relayed connections are a fallback when direct connectivity isn't possible"; "DERP servers are reliable but have limited quality of service"; "Any device that can open an HTTPS connection to an arbitrary host can build a tunnel using DERP relays"; opening UDP 41641 inbound is presented as making direct connections more likely, not as a requirement ([connection types KB](https://tailscale.com/kb/1257/connection-types)).
- NAT: a stateful UDP firewall "allows an inbound UDP packet if it previously saw a matching outbound packet"; hard NATs (endpoint-dependent mapping) "create a completely different NAT mapping for every different destination"; DERP is "our fallback of last resort", "runs over HTTP", and relayed paths have "a bit more latency, maybe less bandwidth" ([How NAT traversal works](https://tailscale.com/blog/how-nat-traversal-works)). No latency figure is given on that page: **not found**.
- Consequence stated by the docs for a `Listen` on a tsnet node: it listens "only on the Tailscale network" ([pkg.go.dev tsnet](https://pkg.go.dev/tailscale.com/tsnet)); the Tailscale blog says such a service is reachable only over Tailscale and "there's no way to get into [it] from outside your tailnet" ([tsnet virtual private services](https://tailscale.com/blog/tsnet-virtual-private-services)). A tsnet listener therefore is not a listener on the host's network interfaces (userspace stack, per the KB).
- Whether a peer can reach an outbound-only node's tsnet listener through DERP alone: the KB statements above say DERP relays tunnels for devices that can open HTTPS; no page read states in one sentence that inbound tsnet `Listen` connections are served over DERP when no direct path exists. **Unconfirmed** as a single quoted statement.
- Tagged nodes: "A tagged device's identity is the combination of all its tags"; "Applying a tag to a device removes any user-based authentication"; key expiry is disabled by default for tagged devices ([Tailscale tags KB](https://tailscale.com/kb/1068/tags)). Headscale-specific tag behaviour (tagged pre-auth keys, `tagged-devices` owner, expiry exemption) is in `headscale-derp-hosting.md` section 4.
- Default-deny: Headscale policy omission means allow-all, and `"grants": []` denies all (see `headscale-derp-hosting.md` section 4, [Policy](https://headscale.net/stable/ref/policy/)). Whether Tailscale/Headscale ACL rules are stateful, so that a reply to a connection the Agent opened needs no Agent-to-control-plane rule: **unconfirmed** (not read in this pass). The task premise says Agents cannot reach each other; it does not say whether Agents may reach the control plane over the tailnet.

### 1.3 Peer identity: WhoIs and tags

- `local.Client.WhoIs(ctx, remoteAddr string) (*apitype.WhoIsResponse, error)` and `WhoIsNodeKey(ctx, key.NodePublic)` exist; `ErrPeerNotFound` is returned "when a peer is not found" ([client/local v1.104.1](https://pkg.go.dev/tailscale.com/client/local)). The package-level `WhoIs` is deprecated in favour of `Client.WhoIs` (same page). The prose doc comment of `WhoIs` beyond the signature was not retrievable in this pass (the page was truncated): **unconfirmed** wording.
- `apitype.WhoIsResponse`: "In successful whois responses, Node and UserProfile are never nil"; `CapMap` is "a map of capabilities to their values" ([apitype.go](https://raw.githubusercontent.com/tailscale/tailscale/main/client/tailscale/apitype/apitype.go), main branch, not pinned).
- `tailcfg.Node.Tags`: "Tags are the list of ACL tags applied to this node", form `tag:<value>`; `Node.User` "doesn't reflect the ACL identity that the node is running as" when ACL tags are in use; `PeerCapMap` values "are passed through from the ACLs to the application via the WhoIs API" ([tailcfg.go](https://raw.githubusercontent.com/tailscale/tailscale/main/tailcfg/tailcfg.go), main branch).
- The Tailscale blog sample calls `lc.WhoIs` with each request's remote address and uses the returned login and node name ([blog](https://tailscale.com/blog/tsnet-virtual-private-services)).
- Applies to strategy (b) in both directions (Agent authenticating the control plane, control plane authenticating the Agent when it is the listener). For strategies where the Agent dials a public hostname (not the tailnet), WhoIs does not apply: no source read says otherwise.
- Whether Headscale populates `Node.Tags` and `CapMap` in the map it sends peers identically to Tailscale's control plane: **unconfirmed** (not read in Headscale source).

### 1.4 How a service learns a peer's tailnet IP

- Headscale REST `GET /api/v1/node` (ListNodes; optional `user` filter, field 1) and `GET /api/v1/node/{node_id}` (GetNode) ([headscale.proto v0.29.5](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/proto/headscale/v1/headscale.proto)). The API docs page itself does not list node endpoints; it points to `/swagger` ([API](https://headscale.net/stable/ref/api/)).
- `Node` message ([node.proto v0.29.5](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/proto/headscale/v1/node.proto)): `id` (1), `name` (6), `given_name` (21), `ip_addresses` (5, repeated string), `tags` (26), `online` (22), `last_seen` (8).
- MagicDNS: FQDN is `hostname.base_domain`; `base_domain` "must be different from the server_url domain" and a FQDN without trailing dot; clients need `--accept-dns=true` ([config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)). How `given_name` maps to the hostname label is not stated on the DNS docs page ([DNS](https://headscale.net/stable/ref/dns/)): **unconfirmed**. Headscale 0.29.0 changed hostname collision suffixes to numeric, so names can change on upgrade (per `headscale-derp-hosting.md` section 6).
- Whether a tsnet node's `Dial("tcp", "name:port")` resolves MagicDNS names from Headscale: **unconfirmed** (tsnet pages do not say).
- Headscale `online` and `last_seen` reflect the control connection (long poll); the exact semantics of `online` were not read: **unconfirmed**. Related fixed bugs: [#3410](https://github.com/juanfont/headscale/issues/3410) (node deletion did not close the active long-poll session, closed 2026-09-09) and [#3535](https://github.com/juanfont/headscale/issues/3535) (healthy node deleted by a race, closed 2026-10-08); issue titles only.

### 1.5 Keepalive and dead-connection detection (RFC and library text)

| Layer | What the source says | Source |
|---|---|---|
| TCP keep-alive | "Implementors MAY include 'keep-alives'"; they "MUST default to off"; interval "MUST be configurable and MUST default to no less than two hours"; an implementation "MUST NOT interpret failure to respond to any specific probe as a dead connection". | [RFC 1122 §4.2.3.6](https://www.rfc-editor.org/rfc/rfc1122.txt) |
| Go TCP dialer | `Dialer.KeepAlive`: if zero, probes are sent "with a default value (currently 15 seconds)"; negative disables. `KeepAliveConfig` idle/interval/count defaults not retrieved: **unconfirmed**. | [net.Dialer, Go 1.27.3](https://pkg.go.dev/net#Dialer) |
| HTTP/2 PING | PING determines "whether an idle connection is still functional"; receiver "MUST" reply with PING+ACK with identical 8-octet payload; responses "SHOULD be given higher priority"; GOAWAY is "best-effort"; the RFC defines no keepalive timer or dead-connection timeout. | [RFC 9113 §6.7, §6.8, §5.4.1](https://www.rfc-editor.org/rfc/rfc9113.txt) |
| Go HTTP/2 | `Transport.ReadIdleTimeout`: ping-based health check if no frame received, zero disables; `PingTimeout` default 15 s. These fields are marked deprecated in favour of `http.Transport.HTTP2` settings. | [x/net/http2 v0.61.0](https://pkg.go.dev/golang.org/x/net/http2) |
| WebSocket | "Upon receipt of a Ping frame, an endpoint MUST send a Pong frame"; a Pong "MAY be sent unsolicited" as "a unidirectional heartbeat"; Ping "may serve either as a keepalive or as a means to verify that the remote endpoint is still responsive". No timeout is defined. | [RFC 6455 §5.5.2, §5.5.3](https://www.rfc-editor.org/rfc/rfc6455.txt) |
| gRPC | See section 4.3. | grpc.io |
| NATS | Server sends `PING`; if the client does not answer within the configured response interval the server closes the connection; normal traffic counts as keep-alive. | [NATS protocol](https://docs.nats.io/reference/reference-protocols/nats-protocol) |
| MQTT | Client must send PINGREQ if it sent nothing within Keep Alive seconds; server closes if nothing is received "within one and a half times the Keep Alive time period"; 0 disables. | [MQTT v5.0 §3.1.2.10](https://docs.oasis-open.org/mqtt/mqtt/v5.0/mqtt-v5.0.html) |
| SSE | No ping. Authors should send a comment line (starting `:`) "every 15 seconds or so" against proxies that drop idle connections. | [WHATWG HTML Standard, server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html) |

NAT idle-timeout values on home routers: **not found** in any primary source read.

## 2. Strategy (a): Agent pull or long-poll over HTTP

**Connection direction and listener.** The Agent opens every connection (HTTP request to the control plane). The Agent needs no listener. The target can be a public hostname on tcp/443 or, if the control plane is a tailnet node and policy allows Agent to control-plane traffic, its tailnet address through `tsnet.Server.Dial` (section 1.2 gap on ACL statefulness). HTTP has no server-to-client push in this strategy; work waits until the next request from the Agent.

**Request flow (derived from the task description, not a source).** Edge proxy to control plane; control plane queues the request; the Agent's outstanding poll returns the request (env, stdin body); the Agent runs the WASI module; the Agent POSTs the response (up to 10 MiB) back; the control plane answers the edge. Hops: edge to control plane, control plane to Agent (via the poll response), Agent to control plane (response POST), control plane to edge.

**Latency in principle.** The sources say nothing specific to polling latency. In principle (my reading, not a source statement): if a poll is already parked, delivery waits for no timer; otherwise it waits for the Agent's next poll. HTTP/1.1 vs HTTP/2 connection reuse behaviour: [RFC 9113](https://www.rfc-editor.org/rfc/rfc9113.txt) (multiplexed streams on one connection; `SETTINGS_MAX_CONCURRENT_STREAMS` "no smaller than 100" recommended, §6.5.2). Measured per-hop latency: **not found**.

**Disconnect, reconnect, backoff.** HTTP defines none of this; it is client policy. RFC 9110 §9.2.2 states that for idempotent methods "the request can be repeated automatically if a communication failure occurs" and the client may open a new connection (excerpt read; method list not quoted: **unconfirmed** which methods the RFC lists) ([RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.txt)). RFC 9110 mentions `Retry-After` for 413 and 503 (§15.5.14, §15.6.4; §10.2.3 definition not read). Dead-connection detection relies on TCP keepalive (default off per RFC 1122; Go default 15 s per `net.Dialer`) or HTTP/2 PING (section 1.5).

**Ordering and delivery.** RFC 9110 text read does not state any ordering guarantee between separate requests: **unconfirmed**. A response POST lost mid-flight cannot be known delivered unless the control plane acknowledges; idempotency options are in section 8.

## 3. Strategy (b): control-plane push to an Agent listener on the tailnet address

**Connection direction and listener.** The control plane initiates (`Server.Dial` to the Agent's tailnet IP or MagicDNS name); the Agent must run a listener: `tsnet.Server.Listen` on the Agent, which "announces only on the Tailscale network" ([pkg.go.dev tsnet](https://pkg.go.dev/tailscale.com/tsnet)). No public listener on the internet. The Agent still opens outbound connections to Headscale (control channel) and, if needed, a DERP relay, so the premise holds. The path between the two nodes is direct (NAT traversal) or via DERP (section 1.2).

**Request flow (derived).** Edge proxy to control plane; control plane resolves the Deployment's pinned Agent, looks up its tailnet IP (section 1.4), dials it over the tailnet, sends the request; Agent runs the module; response returns on the same connection. The Agent can authenticate the caller with WhoIs on the remote address (section 1.3), e.g. by tag on the control plane's node: the mechanism exists, the tag choice is policy.

**Latency in principle.** Added hops: the tailnet leg (WireGuard in userspace on both ends, per the tsnet KB) and, if no direct path, the DERP relay. Docs: direct is "lowest latency", relay adds "a bit more latency, maybe less bandwidth" (section 1.2). Connection setup cost per request vs connection reuse: not covered by tsnet docs. Numbers: **not found**.

**Disconnect, reconnect, backoff.** If the Agent is offline, `Dial` to its address fails; the glossary already defines the result (edge answers 503). Reconnection of the node to Headscale and DERP is internal to the Tailscale client; documented backoff values: **not found** in the pages read. The control plane must pick its own retry policy for `Dial`. Headscale ephemeral-node cleanup (30 m inactivity default) is in `headscale-derp-hosting.md` section 3.

**Ordering and delivery.** Each request is a TCP stream over the tailnet; TCP in-order delivery within a connection is RFC 9293 behaviour (not fetched here: **unconfirmed** by quote). Across requests no ordering applies. A request in flight when the connection breaks has unknown execution state; RFC 9110 §9.2.2 above governs automatic retry for idempotent methods.

**Operational note from sources.** The control plane would be a tailnet node too (tsnet or system client); registering requires an auth key (section 1.1, `headscale-derp-hosting.md` section 4).

## 4. Strategy (c): bidirectional streaming over one Agent-initiated connection

Common to all three variants: the Agent dials out, no Agent listener, the control plane multiplexes requests onto the open connection and the Agent returns responses on it. Request flow (derived): edge proxy to control plane; control plane writes the request to the Agent's open stream/socket; Agent runs module; response written back on the same connection; control plane answers the edge. The Agent can connect to a public tcp/443 endpoint or to the control plane's tailnet address.

### 4.1 gRPC bidirectional streaming

- Direction: "the call is initiated by the client invoking the method"; the two streams are independent ([core concepts](https://grpc.io/docs/what-is-grpc/core-concepts/)). In the HTTP/2 mapping every call starts with client Request-Headers ([PROTOCOL-HTTP2.md](https://raw.githubusercontent.com/grpc/grpc/master/doc/PROTOCOL-HTTP2.md)). So the Agent is the gRPC client and the control plane the gRPC server; the control plane sends work as server-to-client stream messages.
- Ordering: "The order of messages in each stream is preserved" (core concepts). No cross-stream ordering statement read.
- Delivery on failure: "If a detectable connection failure occurs on the client all calls will be closed with an UNAVAILABLE status" ([PROTOCOL-HTTP2.md](https://raw.githubusercontent.com/grpc/grpc/master/doc/PROTOCOL-HTTP2.md)); an expired client-initiated PING "will cause all calls to be closed with an UNAVAILABLE status"; server-initiated PING timeout closes calls with CANCELLED. GOAWAY is sent by servers; streams after the last accepted stream are UNAVAILABLE (same doc). Cancellation "does not roll back" earlier changes (core concepts). There is no built-in resume of a stream: the client opens a new call (inference from the above, not stated verbatim).
- Reconnect backoff (channel connection, [connection-backoff.md](https://raw.githubusercontent.com/grpc/grpc/master/doc/connection-backoff.md), master): `INITIAL_BACKOFF` 1 s, `MULTIPLIER` 1.6, `JITTER` 0.2, `MAX_BACKOFF` 120 s, `MIN_CONNECT_TIMEOUT` 20 s. Whether the Go implementation uses these exact values by default was not checked: **unconfirmed**.
- Keepalive documented defaults ([gRPC keepalive guide](https://grpc.io/docs/guides/keepalive/), C-core-style names): `KEEPALIVE_TIME` client default INT_MAX (disabled), server 7200000 ms (2 h); `KEEPALIVE_TIMEOUT` 20000 ms both; `KEEPALIVE_WITHOUT_CALLS` client 0; `PERMIT_KEEPALIVE_WITHOUT_CALLS` server 0; `PERMIT_KEEPALIVE_TIME` server 300000 ms (5 min), described as the minimum allowed time between successive pings without data. If a peer sends pings the server does not permit, "the first few keepalive pings will be ignored, and the server will eventually send a GOAWAY" with debug data `too_many_pings`. The guide lists no Go-specific defaults.
- Go package ([grpc/keepalive v1.84.0](https://pkg.go.dev/google.golang.org/grpc/keepalive)): `ClientParameters.Time` has no stated default and values under 10 s are raised to 10 s; `Timeout` default 20 s when keepalive is enabled; `PermitWithoutStream` default false (pings stop when no RPC is active). `ServerParameters`: `Time` default 2 hours, `Timeout` 20 s, `MaxConnectionIdle`/`Age`/`AgeGrace` infinity. `EnforcementPolicy.MinTime` default 5 minutes, `PermitWithoutStream` false. Consequence stated by these defaults: a client that pings more often than the server's `MinTime` without active calls is subject to GOAWAY `too_many_pings`; a long-lived idle Agent stream counts as an active stream while the call is open (that reading is inferred: **unconfirmed**).
- Performance guidance: streams "cannot be load balanced once they have started"; "Use keepalive pings to keep HTTP/2 connections alive during periods of inactivity"; per-connection concurrent-stream limits queue RPCs on the client ([performance guide](https://grpc.io/docs/guides/performance/)).
- Proxy behaviour: gRPC through a reverse proxy is Headscale's own unsupported feature for its gRPC API ([API docs](https://headscale.net/stable/ref/api/)); that concerns Headscale's endpoint, not Janus's. Whether the chosen edge/TLS front end carries HTTP/2 gRPC: **unconfirmed** (not in scope of sources read).

### 4.2 WebSocket

- Direction: the client sends an HTTP Upgrade request to the server on a normal HTTP(S) port; RFC 6455 opening handshake detail not re-read in this pass: **unconfirmed** by quote.
- Liveness: Ping/Pong as in section 1.5; RFC 6455 defines no interval or timeout, so the application chooses them.
- Disconnect and reconnect: not defined by RFC 6455 beyond closing handshake; backoff is application policy. Abnormal-closure sections (7.1.5, 7.1.7) were not retrievable: **unconfirmed**.
- Ordering: one TCP connection, frames in order; the RFC's explicit ordering statement was not quoted in this pass: **unconfirmed**. Message-level acknowledgement does not exist at the protocol level; an application protocol would have to add it.
- Headscale's own control channel uses WebSockets behind proxies (`Upgrade: tailscale-control-protocol`) and Cloudflare Proxy/Tunnel are unsupported for Headscale ([reverse proxy](https://headscale.net/stable/ref/integration/reverse-proxy/), per `headscale-derp-hosting.md`). That is about Headscale's port, not Janus's own endpoint.

### 4.3 SSE plus POST

- Direction: SSE is server-to-client over one long-lived HTTP response that the client opened; client-to-server messages need separate requests (POST). Both are Agent-initiated if the Agent opens the SSE GET and POSTs results. The HTTP method used by `EventSource` is not specified in the section read ("GET only" is not confirmed by that content): **unconfirmed** ([WHATWG](https://html.spec.whatwg.org/multipage/server-sent-events.html)). The Agent is a Go program, not a browser; whether it uses `EventSource` semantics or a hand-written client is a design point, not a source fact.
- Reconnect: "Clients will reconnect if the connection is closed"; initial reconnection time is "[implementation-defined], probably in the region of a few seconds"; the server may change it with a `retry` field (ASCII digits only); `Last-Event-ID` is sent on reconnect when the last ID is non-empty; HTTP 204 tells the client to stop reconnecting; a non-200 status or wrong Content-Type fails the connection and "it does not attempt to reconnect" (same page).
- Delivery: the `id` field gives resumption by event ID; whether events are replayed is up to the server, as the spec only defines the header.
- Ordering: single response stream; POSTs are separate HTTP requests with no ordering guarantee in the RFC 9110 text read (**unconfirmed**).
- Liveness: comment line every "15 seconds or so" (section 1.5).

**Latency in principle for all of (c):** no per-request connection setup once the stream is open; the sources describe this as the motivation for streams ("Streams can avoid continuous RPC initiation", [performance guide](https://grpc.io/docs/guides/performance/)). Measured numbers: **not found**.

## 5. Strategy (d): message broker (NATS, MQTT)

A broker adds a third party: both the control plane and the Agent connect to it, so it needs a listener reachable by the Agent. The sources below do not say where it would run for Janus.

### 5.1 NATS

- Direction: the client opens a TCP connection first, the server sends `INFO`, the client replies `CONNECT`; transports are TCP and WebSocket ([protocol](https://docs.nats.io/reference/reference-protocols/nats-protocol)). The Agent needs no listener. The NATS server needs a client listener (default port 4222 per the leafnodes page). Leaf nodes: a leaf "opens an outbound connection to a remote NATS system"; the hub "opens a port for leaf nodes to dial" (default 7422); a leaf listing several hub URLs reconnects to another if one is down ([leafnodes](https://docs.nats.io/running-a-nats-service/configuration/leafnodes)). WebSocket support for leaf connections: **unconfirmed**.
- Core NATS delivery: "core NATS is at-most-once"; publishing is "fire-and-forget"; "The server does not store it for later" ([pub/sub](https://docs.nats.io/nats-concepts/core-nats/pubsub)). A subscriber that is offline never receives the message.
- JetStream: "at-least-once delivery" because unacknowledged messages are redelivered ([JetStream](https://docs.nats.io/nats-concepts/jetstream)). Pull consumers deliver "the next message to a worker when the worker asks" with explicit ack; `max_ack_pending` default 1000 ([consumers](https://docs.nats.io/nats-concepts/jetstream/consumers)). Ack wait default, redelivery mechanics and ordering guarantees were not on the pages read: **unconfirmed**. Duplicate window default "2m0s" using the `Nats-Msg-Id` header: duplicates within the window are stored once ([streams](https://docs.nats.io/nats-concepts/jetstream/streams)). Retention modes: Limits (default), Interest, WorkQueue (same page).
- Request-reply: the pub/sub page lists the topics (`_INBOX` reply subject, timeouts, no-responders) but states no delivery guarantee for it.
- Reconnect (client library): nats.go v1.54.0 constants: `DefaultReconnectWait` 2 s, `DefaultMaxReconnect` 60, `DefaultPingInterval` 2 min, `DefaultMaxPingOut` 2, `DefaultReconnectBufSize` 8 MB, `DefaultTimeout` 2 s ([pkg.go.dev nats.go](https://pkg.go.dev/github.com/nats-io/nats.go)). The docs.nats.io reconnect page did not give these values (the fetched page was only an index to a "resilient clients" section): behaviour description from docs.nats.io **not found** in this pass.
- Payload: `max_payload` is reported by the server in `INFO`; no documented default on the page; the demo server's example showed 1048576 bytes. The 10 MiB body limit in the Janus premise is therefore not covered by that observed value: the documented default and configurable maximum were **not found**.
- Latency in principle: each request is Publisher to broker to Agent, and the response returns the same way (two hops through the broker). No numbers: **not found**.

### 5.2 MQTT

- Direction: client-initiated connection to the broker (CONNECT). Keep Alive rules in section 1.5.
- Delivery (v3.1.1, [spec](https://docs.oasis-open.org/mqtt/mqtt/v3.1.1/os/mqtt-v3.1.1-os.html)): QoS 0 "either once or not at all"; QoS 1 "at least once", sender keeps the message until PUBACK; QoS 2 "exactly once" via PUBREC, PUBREL, PUBCOMP. §4.4: resending after reconnect with CleanSession 0 "is the only circumstance where a Client or Server is REQUIRED to redeliver messages". §4.6: "A Server MUST by default treat each Topic as an 'Ordered Topic'" and deliver in the order received from a given client.
- Sessions (v5.0, [spec](https://docs.oasis-open.org/mqtt/mqtt/v5.0/mqtt-v5.0.html)): Clean Start 1 discards any existing Session; 0 resumes it. Session Expiry Interval absent or 0 ends the session when the connection closes; 0xFFFFFFFF never expires. The v5.0 text for §4.3 (QoS), §4.4 and §4.6 was not retrievable by the fetch tool; v3.1.1 text above is used and v5 equivalence is **unconfirmed**.
- Reconnect backoff: the MQTT specs read define none; it is client library policy.
- Latency in principle: as NATS, two broker hops; no numbers **not found**.
- Whether the broker supports request-response semantics: MQTT v5 request/response properties were not read: **unconfirmed**.

## 6. Artifact fetch by content hash (applies to every strategy)

The Artifact is a WASM file that the Agent fetches over HTTP and verifies by hash (per glossary). The HTTP semantics:

- **Range requests:** RFC 9110 §14.2 `Range = ranges-specifier`; "A server MAY ignore the Range header field"; §14.3 `Accept-Ranges` indicates whether an upstream server supports range requests, `Accept-Ranges: none` is the reserved value; §14.4 `Content-Range`; §15.5.17 a 416 response SHOULD carry `Content-Range` ([RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.txt)). The 206 section (§15.3.7) text was not retrievable. A server may therefore return the full body with 200.
- **Resume with validator:** `If-Range` "allows a client to short-circuit the second request"; the client "MUST NOT generate an If-Range header field containing an entity tag that is marked as weak"; "A recipient of an If-Range header field MUST ignore the Range header field if the If-Range condition evaluates to false" and then responds 200 with the full representation (RFC 9110 §13.1.5, excerpt).
- **ETag:** "An entity tag consists of an opaque quoted string, possibly prefixed by a weakness indicator"; "strong being the default" (§8.8.3). A strong validator "changes value whenever a change occurs to the representation data" (§8.8.1). ETag is a validator, not a statement that the tag is a content hash; using the SHA-256 hex as the ETag value is a server choice (the RFC says the string is opaque).
- **Digest headers (RFC 9530):** `Content-Digest` covers "the actual message content" (the bytes sent); `Repr-Digest` covers "the entire selected representation data" and exists to support "validating the integrity of a resource that was reconstructed from parts" retrieved in multiple requests; `Want-Repr-Digest` is only "a hint"; `sha-256` is Active and "RECOMMENDED" (§7.2, §5). In a 206 response, `Content-Digest` covers only the partial bytes while `Repr-Digest` covers the whole object (Appendix B.3) ([RFC 9530](https://www.rfc-editor.org/rfc/rfc9530.txt)). The Agent still has to verify the assembled file against the hash it holds; RFC 9530 headers are sent by the server and are not required to be present.
- Which strategy carries the Artifact: not stated by any source; the fetch is plain HTTP in all four (inference). Over the tailnet, DERP is a fallback with "limited quality of service" (section 1.2) and its throughput statement for Headscale's embedded relay is "no speed or throughput optimisations" (see `headscale-derp-hosting.md` section 5).
- Artifact size limit: the premise states 10 MiB bodies; no source read links that to the Artifact size.

## 7. Usage numbers: batching and idempotency

Usage fields from the premise: fuel, peak memory, duration, bytes, status per invocation.

- **Idempotency-Key header:** an IETF Internet-Draft, not an RFC. `draft-ietf-httpapi-idempotency-key-header-07` (latest revision 2025-10-15; the Datatracker shows state "Expired", intended status none; last updated 2026-04-18). The abstract says the header "can be used to make non-idempotent HTTP methods" fault-tolerant (POST, PATCH). The normative sections on duplicate-key handling and the 400/409/422 responses were not in the fetched page: **unconfirmed** ([Datatracker](https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/)).
- **RFC 9110 §9.2.2:** idempotent methods have the same intended effect when repeated and "can be repeated automatically if a communication failure occurs". Which methods the RFC lists as idempotent (PUT, DELETE per the RFC) was not quoted in this pass: **unconfirmed** by quote.
- **NATS JetStream:** `Nats-Msg-Id` deduplication in a 2-minute default window (section 5.1). Retries outside the window are not deduplicated by that mechanism.
- **MQTT:** QoS 2 "exactly once" is scoped to the protocol exchange between two parties (v3.1.1 §4.3.3); QoS 1 "at least once" can deliver duplicates.
- **gRPC:** a call that fails with UNAVAILABLE gives no statement about whether the server processed it (PROTOCOL-HTTP2.md says only "all calls will be closed with an UNAVAILABLE status" on connection failure; retry policy docs were not read: **unconfirmed**).
- **Batching:** no RFC or protocol text read prescribes batch size or flush interval for usage reports. HTTP/2 allows many concurrent streams on one connection (RFC 9113 §6.5.2). A request body limit for any chosen endpoint is a deployment setting: **not found** as a protocol constant.

## 8. Summary matrix (facts only)

| Property | (a) Pull / long-poll | (b) Push to tailnet listener | (c) gRPC stream | (c) WebSocket | (c) SSE + POST | (d) NATS | (d) MQTT |
|---|---|---|---|---|---|---|---|
| Who opens the connection | Agent | Control plane | Agent | Agent | Agent (GET) and Agent (POST) | Agent and control plane both to broker | Agent and control plane both to broker |
| Agent listener | No | Yes, tsnet `Listen` on the tailnet only | No | No | No | No | No |
| Third-party listener | No | No | No | No | No | Yes, broker | Yes, broker |
| Liveness mechanism | TCP keepalive, HTTP/2 PING | Tailnet path handled by Tailscale client; app-level via TCP | HTTP/2 PING, keepalive params | WS Ping/Pong (no timeout defined) | Comment lines, TCP | PING/PONG | PINGREQ, 1.5x Keep Alive |
| Reconnect policy defined by source | No | No | Yes, backoff doc (master) | No | Yes, reconnect, `Last-Event-ID`, `retry` | Library constants (nats.go v1.54.0) | No |
| Ordering stated by source | Not stated | Not stated | Within a stream | Not quoted | Not stated for POSTs | Not stated (core); JetStream not read | Per topic, by default (v3.1.1) |
| Delivery semantics stated | Not stated | Not stated | UNAVAILABLE/CANCELLED on failure | None at protocol level | None | Core at-most-once; JetStream at-least-once | QoS 0, 1, 2 |
| Dedup mechanism in source | Idempotency-Key draft (expired) | same | same | same | same | `Nats-Msg-Id`, 2 min | QoS 2 handshake |

## Open items

1. tsnet against Headscale: no primary page states support. A live test with the pinned `tailscale.com` module and Headscale v0.29.5 is the only way to confirm (also checks section 1.1 version inference and MagicDNS resolution from `Dial`).
2. Whether an inbound tsnet `Listen` connection works through DERP when neither node has inbound reachability, as a quoted primary statement; and whether Headscale ACL/grant rules are stateful (Agent replying on a control-plane-initiated flow needs no reverse rule; Agent-initiated flows to the control plane need a rule).
3. Per-hop and relay latency, NAT idle timeouts, and DERP throughput: no primary numbers found.
4. Primary text not retrievable by the fetch tool in this pass: RFC 9110 §14.2 body and §15.3.7 (206), §10.2.3 (Retry-After), RFC 6455 §7.1.5 and §7.1.7 and the handshake, MQTT v5.0 §4.3, §4.4, §4.6, the Idempotency-Key draft body, `local.Client.WhoIs` doc comment, NATS ack wait/redelivery/ordering and reconnect docs, and gRPC retry docs.
5. Go gRPC implementation of connection backoff defaults (the doc read is the C-core design doc on master).
6. Whether the edge/TLS front end Janus uses can carry HTTP/2 gRPC, WebSockets and long-lived SSE responses to the control plane: no source read.
