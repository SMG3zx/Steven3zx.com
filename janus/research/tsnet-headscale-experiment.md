# tsnet against Headscale: experiment results

Run on 2026-10-20 for Linear ENG-78. Measured facts from one local run, not documentation. It settles items that `agent-protocol-strategies.md` marked **unconfirmed**. No recommendation here.

**Setup.** Headscale v0.29.5 (`headscale/headscale:v0.29.5`) in Docker on one Windows machine, SQLite, default DERP map URL, no embedded DERP, `server_url` `http://127.0.0.1:8080`, MagicDNS base domain `janus.test`, policy from a file. Three `tailscale.com/tsnet` v1.104.1 nodes (Go 1.27.1) on the same machine, each with its own state directory: `ctrl` (`tag:ctrl`), `agent-a` (`tag:acct-a`), `agent-b` (`tag:acct-b`). Each node used a single-use pre-auth key made with `headscale preauthkeys create --user 1 --tags tag:<x> --expiration 1h`, passed in `tsnet.Server.AuthKey`, with `ControlURL` set to the Headscale URL. Source: `tsnet-headscale-probe/`. The probe has no keys in it.

**Limits of this run.** All nodes were on one host with loopback paths, so there was no NAT, no real DERP relay and no real network latency. Nothing here measures DERP, NAT traversal, router port forwarding or throughput. Port 443 and 3478 reachability from the internet was not tested.

## Results

| # | Question | Result |
|---|---|---|
| 1 | Does a tsnet node enroll against Headscale with `ControlURL` and a tagged pre-auth key? | Yes. All three nodes came up. `Status.Self.Tags` showed `tag:ctrl`, `tag:acct-a`, `tag:acct-b`. `headscale nodes list` showed them as `tagged-devices` and online. |
| 2 | Can the control plane dial an Agent's tsnet `Listen` by MagicDNS short name? | Yes, `http://agent-a:9000` and `http://agent-b:9000` from `ctrl` worked with `tsnet.Server.HTTPClient()`, with policy rule `tag:ctrl` to `tag:acct-a:*` and `tag:acct-b:*` only. |
| 3 | Does `WhoIs` on the Agent identify the caller by tag? | Yes. `lc.WhoIs(ctx, r.RemoteAddr)` returned `Node.ComputedName` `ctrl` and `Node.Tags` `["tag:ctrl"]`. The reverse worked too: the control plane saw `agent-a` with `["tag:acct-a"]`. |
| 4 | Are the ACLs stateful (does a reply to a connection the control plane opened need an Agent-to-control-plane rule)? | Yes, in this run. With no rule from any Agent to `tag:ctrl`, the control plane's requests to Agents completed with full responses. |
| 5 | With that policy, can an Agent open a connection to the control plane? | No. `agent-a` and `agent-b` dialing `ctrl:8000` timed out at the client's 5 s timeout. |
| 6 | Can an Agent reach another Account's Agent? | No. The name did not resolve (`lookup agent-b: no such host`). Under that policy, an Agent's network map did not contain the other Agent. I did not dial the other Agent's IP address directly. |
| 7 | What did a pull-style rule cost? | One added rule, `tag:acct-a` to `tag:ctrl:8000`, plus `docker kill -s HUP` to reload the policy file (log: "Policy reload completed with changes"). After that `agent-a` reached `ctrl:8000` and the control plane's `WhoIs` returned `agent-a` with `tag:acct-a`. `agent-b`, with no rule, still timed out. Existing nodes needed no restart. |
| 8 | How quickly does Headscale mark a killed Agent offline? | About 10 s. The process was force-killed at 13:40:34 local; `headscale nodes list` at 13:40:59 showed `offline` with last seen 13:40:44. This was a force-killed process on loopback, so the TCP close was seen at once. A dead network path may take longer. |
| 9 | What does the control plane see when an Agent is dead? | The request fails only when the client timeout fires (5 s here). There was no fast error. |
| 10 | Does an Agent come back from a restart without a new key? | Yes. `agent-b` restarted from its saved state directory with no new key and was reachable from the control plane about 7 s after the process started (started 13:41:33, first success 13:41:40). |

## gRPC bidirectional stream over tsnet

Run later the same day on the same setup, with `google.golang.org/grpc` v1.84.0. Policy was the spec's: `tag:ctrl` to the Agent tags only, no rule from an Agent to the control plane. The control plane (a gRPC client) opens one bidirectional stream to the Agent (a gRPC server on a tsnet `Listen`). gRPC dials through `tsnet.Server.Dial` via `grpc.WithContextDialer`. Messages are `StringValue`; the service is registered by hand, with no generated code. Source: `tsnet-grpc-probe/`. Same limits as above: one host, loopback, no NAT, no DERP.

| # | Question | Result |
|---|---|---|
| 11 | Does a gRPC bidirectional stream work over tsnet with no Agent-to-control-plane rule? | Yes. The control plane sent a command every 3 s and the Agent answered each one and also sent its own event every 5 s, all on the stream the control plane opened. Over the whole run, which includes the kill and restart in rows 14 and 15, the control plane logged 13 acks and 8 events. |
| 12 | Does an idle stream survive with the control plane pinging every 10 s (`Time` 10 s, `Timeout` 5 s, `PermitWithoutStream`) and the Agent's gRPC defaults? | No. About 40 s after it opened, the control plane received GOAWAY `ENHANCE_YOUR_CALM` with debug text `too_many_pings`, and the stream ended with `Unavailable`. The gRPC server's default enforcement policy rejects pings more often than every 5 minutes. A reopened stream was up 1 s later. |
| 13 | Does an idle stream survive the same pinging when the Agent sets `KeepaliveEnforcementPolicy{MinTime: 5s, PermitWithoutStream: true}`? | Yes, for the 90 s run, with no errors and no traffic. |
| 14 | How fast does the control plane notice a force-killed Agent? | 15.0 s after the last frame it received (last frame at 14:01:13.387, stream down at 14:01:28.387, error `keepalive ping failed to receive ACK within timeout`). That is the ping interval plus the ping timeout, 10 s plus 5 s. The Agent process was killed at 14:01:13.587, and the stream stayed open until the ping went unanswered. |
| 15 | How fast does the stream return after the Agent restarts? | The Agent process restarted at 14:01:54.2 and its tsnet node was up at 14:01:56.0. The stream was reopened at 14:02:05.4, 11.2 s after the restart. The control plane had kept one `WaitForReady` call waiting for about 36 s. The delay after the Agent was up (9.4 s) is gRPC's client reconnect backoff, which had grown while the Agent was down; the tailnet path was not the cause. The backoff cap was not changed or measured. |

## Mistake in the run

The first attempt to kill only `agent-b` matched on its name in the command line, which also matched `ctrl` and `agent-a` (their `-peers` flags name `agent-b`). All three probe processes were killed. Result 8 was read from Headscale and is unaffected. Results 9 and 10 come from a second run in which the control plane was restarted and `agent-a` was already dead, so the first failure in that run was `agent-a`, not an isolated dead Agent. `agent-b` was then restarted alone, which gave result 10.

## Items in `agent-protocol-strategies.md` now settled for this run

- tsnet against Headscale: works for enrollment, tags, `WhoIs`, dial and listen (v0.29.5 and tsnet v1.104.1).
- Headscale populates `Node.Tags` in `WhoIs`: yes.
- MagicDNS short names between permitted peers: resolve with tsnet `HTTPClient()`. Names of peers not permitted by policy do not resolve.
- ACL statefulness: replies on a connection the permitted side opened needed no rule in the other direction.

## Still not tested

- Any path through a DERP relay, hard NAT, or the open router ports.
- Latency, throughput and many concurrent requests.
- Many Agents, or Agent churn.
- Headscale API calls (`GET /api/v1/node`) from the control plane; the CLI was used instead.
- Whether `Listen` on an outbound-only node is reachable through DERP alone.
