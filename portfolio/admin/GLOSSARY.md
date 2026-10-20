# Origin Admin

The private control room for the origin host behind steven3zx.com. It lets the admin see what the machine is doing and act on it.

## Language

**Origin host**:
The one Windows machine that serves steven3zx.com and its subdomains.
_Avoid_: server, box, machine, host (unqualified)

**Admin**:
A trusted person who signs in to Origin Admin. All admins have the same full powers.
_Avoid_: user, operator, owner

**Host agent**:
The process running natively on the origin host that observes it and carries out actions on it.
_Avoid_: collector, daemon, sidecar

**Snapshot**:
The point-in-time picture of the origin host that the host agent produces and the map renders.
_Avoid_: stats, metrics, payload

**Origin map**:
The React Flow view of the origin host built from a snapshot.
_Avoid_: dashboard, graph, diagram

**Action**:
A change an admin makes to the origin host from Origin Admin, such as restarting a container.
_Avoid_: command, operation, control

## Trust

**Session**:
A signed-in browser of an admin. An admin has at most one session at a time; signing in again replaces the earlier session.
_Avoid_: login, token, cookie

**Authenticator code**:
The six-digit time-based code from the admin's phone, required at sign-in and for a reconfirmation.
_Avoid_: OTP, 2FA code, MFA token

**Enrollment link**:
A single-use, short-lived link issued on the origin host that lets an admin pair their phone and so obtain authenticator codes. An admin without a paired phone cannot sign in.
_Avoid_: setup link, invite, activation

**Reconfirmation**:
Proving presence again with an authenticator code immediately before a destructive action.
_Avoid_: step-up, re-auth, sudo mode

**Audit event**:
A permanent record that an admin signed in, signed out, or took an action.
_Avoid_: audit log entry, activity, history

## Viewing

**Live**:
The origin map following the newest frame as it arrives.
_Avoid_: real-time mode, streaming, auto-refresh

**Frame**:
One snapshot together with the requests that arrived since the previous one, as the origin map shows it.
_Avoid_: tick, sample, update

**Freeze**:
Stopping the origin map on the current frame so it can be inspected or scrubbed through, while collection carries on.
_Avoid_: pause, stop, hold

**Cine buffer**:
The recent frames the browser keeps so a frozen origin map can be scrubbed backward and forward frame by frame.
_Avoid_: replay, rewind buffer, playback

## Traffic

**Request trace**:
The full record of one request crossing the origin host's boundary, every hop it passes through with its timing, headers and bodies.
_Avoid_: request log, access log entry, span

**Inbound request**:
A request arriving at the origin host from outside, through Cloudflare and Caddy to an upstream.
_Avoid_: incoming traffic, ingress

**Outbound request**:
A request a process on the origin host makes to something outside it, such as Janus calling GitHub.
_Avoid_: egress, external call

**Upstream**:
The container or origin host port that Caddy hands a request to.
_Avoid_: backend, target, origin

## Record

**History**:
The last 24 hours of snapshot values kept so the origin map can show change over time.
_Avoid_: metrics, time series, trends

**Log line**:
A line of text captured from a container, Caddy, or the Windows Event Log.
_Avoid_: log entry, event, message
