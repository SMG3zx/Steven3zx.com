# Leaf-node bridge (ENG-122)

`leafbridge` carries an Agent's leaf connection to the hub over the tailnet. `ServeHub` runs on the control plane and copies each tailnet connection to the hub's leaf port. `DialHub` runs on an Agent and gives its leaf remote a loopback address, carried to the hub through the Agent's tailnet node.

The unit test uses loopback in place of tsnet:

    go test -count=1 -v ./internal/platform/leafbridge/

The live test joins a Docker Headscale with real tsnet nodes:

    go test -tags live -count=1 -v ./internal/platform/leafbridge/

## What the loopback test builds

- Hub: NATS with JetStream, domain `hub`, leaf port open.
- Agent: NATS with JetStream, domain `agent-1`, one leaf remote.
- Two `Bridge`s (`bridge.go`) carry the leaf's bytes. The hub bridge stands in for the hub's tsnet listener; the Agent bridge stands in for the Agent's tsnet dial. `SetUp(false)` cuts the link.

## What the test shows (passes)

1. The leaf comes up through the two bridges.
2. A request from the hub on `janus.agent.1.command` reaches the Agent and gets its reply.
3. The hub reads the Agent's `USAGE` stream through domain `agent-1`, with a durable consumer that acks each report.
4. With the link down, the hub cannot reach the Agent, and the Agent keeps recording Usage into `USAGE`.
5. When the link comes back, the leaf reconnects on its own and the hub's consumer receives the three reports recorded while it was down, in order.

## What the loopback test does not prove

- **tsnet.** Both bridges run over loopback TCP here. The live test covers tsnet.
- **Credentials.** The servers run in the default account with no JWTs, so nothing checks that an Agent is limited to `janus.agent.<id>.>`, its inbox, `agent_state` key `<id>`, and `modules-<owner id>`. That needs the `JANUS` and `AGENTS` accounts and a test that one Agent cannot read another's subjects.
- **Revocation.** Not covered.
- **Polling.** `waitFor` polls with a sleep, which the service tests must not do. It is fine in a probe.
- **Port choice.** The leaf port is picked by listening and closing, which can race in theory.
