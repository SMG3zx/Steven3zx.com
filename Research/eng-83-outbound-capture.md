# ENG-83: Capturing outbound requests from the origin host and its containers

Linear: ENG-83 (parent map ENG-80, project Origin Admin). Terms follow `portfolio/admin/GLOSSARY.md`: **origin host**, **host agent**, **outbound request**, **request trace**, **admin**.

## Answer in brief

Full request traces (headers and bodies) for outbound requests are only realistic for:

1. **Code we own** (Janus, Origin Admin, any Go tool of ours), through OTel HTTP client instrumentation plus our own header/body capture hook. No CA is needed.
2. **Callers that honour `HTTP(S)_PROXY` and trust a CA we install.** These are most Linux containers we build, Go and curl based tools on Windows, and WinHTTP/WinINet apps once the system proxy is set. A forward proxy with TLS interception (mitmproxy or a small Go proxy) handles them.

For everything else, the honest ceiling is **connection metadata**: process, remote IP and port, the hostname (from DNS or the TLS SNI), bytes, and timing. Windows Update and Delivery Optimization pin their certificates and **must not** be intercepted. Some other Microsoft and vendor clients also ignore proxies or pin. Container traffic that bypasses a proxy cannot be attributed to a container from the Windows side. It all leaves through `com.docker.backend.exe`.

Recommended layers:

| Layer | Callers | Fidelity | CA needed |
|---|---|---|---|
| L0: connection inventory (ETW / IP Helper, run by the host agent) | every native Windows process, plus containers lumped under `com.docker.backend.exe` | metadata only | no |
| L1: OTel client instrumentation | Janus, Origin Admin, our own Go code | full, configurable redaction | no |
| L2: opt-in intercepting forward proxy | containers we configure, selected Windows tools | full headers and bodies | **yes** (admin decision) |
| L3: pass-through allowlist on the proxy | pinned or sensitive hosts (Windows Update, DO, banks, auth) | metadata only (CONNECT host, bytes, timing) | no |

## 1. Forward proxy with TLS interception

**How it works.** mitmproxy's regular mode is an explicit HTTP(S) proxy on port 8080, and clients must be configured to use it ([mitmproxy modes](https://docs.mitmproxy.org/stable/concepts/modes/)). On first start it creates a CA in `~/.mitmproxy`. That CA "is used for on-the-fly generation of dummy certificates for each visited website". `mitmproxy-ca.pem` holds "the certificate **and the private key**" ([mitmproxy certificates](https://docs.mitmproxy.org/stable/concepts/certificates/)). mitmproxy also has a **local capture** mode for Windows (`--mode local:<process>`). A privileged redirector built on WinDivert diverts packets per PID, so the target process needs no proxy settings ([mitmproxy blog, Windows local capture](https://mitmproxy.org/posts/local-capture/windows/)). A Go proxy is an alternative (e.g. built on `net/http` with our own CA). It is more work, but it can emit request traces in exactly our format.

**Getting callers to use it:**

- *Go programs* (ours and third-party): `http.DefaultTransport` sets `Proxy: ProxyFromEnvironment`, which reads `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`. Requests to `localhost` are never proxied ([net/http](https://pkg.go.dev/net/http#ProxyFromEnvironment)). A program that builds its own `Transport` without `Proxy` ignores the variables.
- *WinHTTP callers* (services, e.g. Windows Update, many agents): proxy set machine-wide with `netsh winhttp set advproxy setting-scope=machine ...`. The older `set proxy` is deprecated ([netsh winhttp](https://learn.microsoft.com/en-us/windows/win32/winhttp/netsh-exe-commands)). WinHTTP is the stack for services and impersonation. WinINet is for interactive per-user apps and uses the per-user Internet Options proxy ([WinINet vs WinHTTP](https://learn.microsoft.com/en-us/windows/win32/wininet/wininet-vs-winhttp)). So the system proxy is really two settings. On the origin host today, `netsh winhttp show advproxy` reports `ProxyIsEnabled: false` and `PerUserProxySettings: true`.
- *Containers*: put `proxies` in the Docker client's `~/.docker/config.json`. It injects `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` into **new** containers and builds only, stores the values in plain text, and leaves the engine untouched ([Docker CLI proxy](https://docs.docker.com/engine/cli/proxy/)). Alternatively, set the variables per service in compose.
- *Image pulls*: Docker Desktop's "Containers proxy" governs all `docker pull`/compose pulls. Running-container traffic goes through it **only** with air-gapped container enforcement ([Docker Desktop settings](https://docs.docker.com/desktop/settings-and-maintenance/settings/)). That needs `transparentPorts` and a **Docker Business** subscription ([air-gapped containers](https://docs.docker.com/enterprise/security/hardened-desktop/air-gapped-containers/)). Locally, `docker info` already reports the engine's proxy as `http.docker.internal:3128`, Docker Desktop's internal proxy.

**Trusting the CA:**

- *Windows*: import into the machine Root store. Go on Windows verifies through the system APIs. Setting `SSL_CERT_FILE`/`SSL_CERT_DIR` there turns those APIs off ([crypto/x509](https://pkg.go.dev/crypto/x509#SystemCertPool)). Docker Desktop picks up the host trust store for `docker pull` once it is pointed at the MITM proxy ([Docker CA certs](https://docs.docker.com/engine/network/ca-certs/)).
- *Linux containers*: bake the cert into the image (`/usr/local/share/ca-certificates/` + `update-ca-certificates`). Runtime `docker cp` installs do not survive recreation ([Docker CA certs](https://docs.docker.com/engine/network/ca-certs/)). Go in containers also honours `SSL_CERT_FILE`/`SSL_CERT_DIR` ([crypto/x509](https://pkg.go.dev/crypto/x509#SystemCertPool)). Runtimes with their own stores (Java, Node `NODE_EXTRA_CA_CERTS`, Python `certifi`) need per-runtime steps. Docker notes that "further steps may be required" for some SDKs ([Docker CA certs](https://docs.docker.com/engine/network/ca-certs/)).

**Who ignores proxies or pins:**

- Windows Update metadata connections "are certificate-pinned … it's important that TLS proxies pass these connections without interception". Content download goes over HTTP:80 and P2P:7680 through Delivery Optimization ([Windows Update security](https://learn.microsoft.com/en-us/windows/deployment/update/windows-update-security)). Microsoft's DO proxy guidance likewise requires TLS inspection off for DO endpoints ([DO proxy](https://learn.microsoft.com/en-us/windows/deployment/do/delivery-optimization-proxy)). mitmproxy's own docs name Windows Update as a pinned client that fails under interception, and recommend `ignore_hosts`/`allow_hosts` ([mitmproxy ignore domains](https://docs.mitmproxy.org/stable/howto/ignore-domains/)).
- Programs that construct their own HTTP transport without proxy support, statically configured clients, anything using raw TCP/UDP, and QUIC/HTTP3. An explicit HTTP proxy does not carry QUIC.
- Defender notes that without a machine WinHTTP proxy the OS cannot fetch CRLs, so proxy configuration itself has knock-on effects ([Defender network connections](https://learn.microsoft.com/en-us/defender-endpoint/configure-network-connections-microsoft-defender-antivirus)).

**Assessment:**

- *Coverage*: callers we configure; nothing else unless in local-capture mode.
- *Fidelity*: full headers and bodies.
- *CA cost*: high (section 5).
- *Breakage*: pinned clients, renewals, proxy outage makes every proxied caller fail closed, `NO_PROXY` inconsistencies between tools ([Docker CLI proxy](https://docs.docker.com/engine/cli/proxy/)).
- *Delivery*: the proxy is ours. A mitmproxy addon or a Go proxy hands each flow to the host agent, which forwards it to the admin server like any other request trace.

## 2. Connection-level capture without decryption

Sources (all read-only and native, so they fit inside the host agent):

- **ETW `Microsoft-Windows-TCPIP`** `{2F07E2EE-15DB-40F1-90EF-9D7BA282188A}`. Keywords on this host include `ConnectPath`, `ClosePath`, `SendPath`, `ReceivePath`, `Transfer`, `ProcessIdHint`, `AleRemoteEndpoint` (verified with `logman query providers Microsoft-Windows-TCPIP`). It gives per-connection connect and close events with endpoints and a PID hint. It is a manifest provider, so it needs an admin-level real-time ETW session.
- **ETW `Microsoft-Windows-Kernel-Network`** `{7DD42A49-…}` (IPv4/IPv6 keywords) gives per-process send and receive events with sizes, for byte counts.
- **ETW `Microsoft-Windows-DNS-Client`** `{1C95126E-7EEA-49A9-A3FE-A378B03DDB4D}`. Event 3008 carries `QueryName`, `QueryResults` and the process ID, which maps IPs back to hostnames per process ([winevt-kb DNS-Client](https://winevt-kb.readthedocs.io/en/latest/sources/eventlog-providers/Provider-Microsoft-Windows-DNS-Client.html); [Velociraptor Windows.ETW.DNS](https://docs.velociraptor.app/artifact_references/pages/windows.etw.dns/)). Secondary sources only; verify the field names against the manifest when implementing.
- **`GetExtendedTcpTable`** with `TCP_TABLE_OWNER_PID_*` or `TCP_TABLE_OWNER_MODULE_*` returns local and remote endpoints, state and owning PID or module. It gives no bytes and no hostnames, and polling misses short-lived connections ([GetExtendedTcpTable](https://learn.microsoft.com/en-us/windows/win32/api/iphlpapi/nf-iphlpapi-getextendedtcptable)). This is fine for the snapshot, but ETW is better for traces.
- **WFP**: the platform under Windows Firewall. It has a user-mode API, and inspecting or modifying the stream needs a kernel callout driver ([WFP](https://learn.microsoft.com/en-us/windows/win32/fwp/windows-filtering-platform-start-page)). It is not worth building ourselves.
- **WinDivert**: a signed kernel driver, "installed below the Windows network stack", that diverts, modifies and drops packets for user-mode code. It is LGPLv3/GPLv2 ([WinDivert](https://github.com/basil00/WinDivert)). It is useful only via mitmproxy local mode. Building on it directly means TCP reassembly ourselves and an extra driver install.
- **pcap (Npcap/pktmon)**: full packets but no process attribution. For TLS it adds only what the ClientHello shows (SNI, ALPN). It is not needed if ETW is used.

**What you get:**

- the process (PID, image)
- the remote IP and port
- the hostname, from DNS-Client correlation or the TLS SNI. SNI is hidden if the client uses Encrypted Client Hello.
- bytes sent and received
- connect, close and duration timing

**What you can't get:**

- method, path, query, status, headers and bodies
- which HTTP requests were multiplexed on one connection

**Assessment:**

- *Coverage*: every native process. Container traffic shows only as `com.docker.backend.exe`, because "All outbound container network traffic originates from the `com.docker.backend` process" ([Docker Desktop networking](https://docs.docker.com/desktop/features/networking/)).
- *Fidelity*: metadata.
- *CA cost*: none.
- *Breakage*: none. It is passive.
- *Delivery*: the host agent consumes ETW in-process and emits connection-level request traces.

## 3. Inside Docker Desktop's VM

- **eBPF.** Docker Desktop's VM kernel (`6.18.33.2-microsoft-standard-WSL2` here) allows a privileged container to use BPF. Docker's ECI page shows `bpftool map show` listing kernel maps *without* ECI and returning "Operation not permitted" *with* it ([Enhanced Container Isolation](https://docs.docker.com/enterprise/security/hardened-desktop/enhanced-container-isolation/)). So a privileged `--pid=host --net=host` collector (e.g. an eBPF connect/sendmsg tracer) can attribute connections to containers. This works only while ECI is off, and it is unsupported territory. eBPF still sees ciphertext. Uprobes on OpenSSL can read plaintext, but Go's statically linked `crypto/tls` (our DDNS container, if it is Go) has no shared-library symbol to hook. mitmproxy's eBPF local mode explicitly states that "containers need host networking" and that WSL is unsupported ([mitmproxy modes](https://docs.mitmproxy.org/stable/concepts/modes/)).
- **Sidecar proxy on the compose network.** Run the intercepting proxy as a compose service (e.g. `egress-proxy`). Point other services at it with `HTTPS_PROXY=http://egress-proxy:8080` and bake its CA into our images. This is the supported path. Optionally put callers on an `internal: true` network with only the proxy dual-homed, so a service that ignores the proxy fails loudly rather than leaking past capture.
- **Docker-native transparent interception** (`containersProxy.transparentPorts`) exists but needs Docker Business ([air-gapped containers](https://docs.docker.com/enterprise/security/hardened-desktop/air-gapped-containers/)).

**Assessment:**

- *Coverage*: containers we configure (sidecar) or all containers (eBPF, metadata only).
- *Fidelity*: full through the sidecar. eBPF gives metadata plus container identity.
- *CA*: the sidecar needs the CA in images.
- *Breakage*: the sidecar is a single point of failure for container egress.
- *Delivery*: the sidecar posts to the host agent via `host.docker.internal`, or straight to the admin server.

## 4. Per-app instrumentation (OTel, our Go apps)

`otelhttp.NewTransport` "wraps outbound HTTP(S) requests with a span and enriches it with metrics". It injects trace context into outgoing headers, and the span ends when the body is closed or reaches EOF ([otelhttp](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp)). HTTP client semconv requires `http.request.method`, `server.address`, `server.port` and `url.full`, plus `http.response.status_code` and `error.type` when applicable. Headers (`http.request.header.<key>`) are **opt-in** because "Including all request headers can be a security risk". **No semconv attribute carries bodies**, only opt-in body sizes ([OTel HTTP spans](https://opentelemetry.io/docs/specs/semconv/http/http-spans/)). For full request traces we wrap the transport with our own `RoundTripper` that tees headers and bodies (with a size cap and redaction of `Authorization`, cookies and tokens). We record that on the span or send it alongside.

**Assessment:**

- *Coverage*: only Janus, Origin Admin and other code we own, including GitHub API calls.
- *Fidelity*: full.
- *CA*: none.
- *Breakage*: minimal. One injected `traceparent` header goes to third parties; it can be dropped with a no-op propagator for external hosts.
- *Delivery*: OTLP to an exporter in the admin server or the host agent.

## 5. Security cost of a trusted interception CA

- Anyone holding the CA private key (`mitmproxy-ca.pem`) can impersonate **any** HTTPS site to every process and container that trusts it ([mitmproxy certificates](https://docs.mitmproxy.org/stable/concepts/certificates/)). Docker warns that a compromised MITM CA lets attackers "intercept sensitive data, spoof a trusted service, or perform man-in-the-middle attacks" ([Docker CA certs](https://docs.docker.com/engine/network/ca-certs/)).
- The proxy sees and stores secrets in plaintext: GitHub tokens, Cloudflare API tokens, cookies. Request traces then become a credential store, so redaction must happen before persistence.
- Mitigations:
  - Never put the CA in the Windows machine Root store. Trust it only in the containers and processes we opt in.
  - Use a per-host CA that never leaves the origin host, with a name constraint if the proxy supports it.
  - Use `allow_hosts` (intercept only listed hosts) rather than intercept-all.
  - Keep the key readable only by the proxy's account, and rotate it.

## 6. What full inspection is realistic per caller

| Caller | Full headers and bodies? | How |
|---|---|---|
| Janus, Origin Admin (our Go) | **Yes** | L1 OTel + body tee; no CA |
| Our containers (e.g. DDNS → Cloudflare) | **Yes**, if we set `HTTPS_PROXY` and bake in the CA | L2 sidecar proxy |
| Third-party containers | Usually, if they honour the proxy env and the CA can be added; otherwise metadata via eBPF (ECI off) | L2 / sidecar, else L0 |
| Docker image pulls | Possible through the Containers proxy + host CA, but low value | Docker Desktop proxy setting |
| Native Windows tools (curl, git, Go CLIs) | Yes, per process via env or mitmproxy local mode + CA | L2 |
| Windows services (WinHTTP) | Mostly **no**: needs the machine proxy + machine Root CA, and WU/DO are pinned | L0 metadata; L3 pass-through |
| Windows Update / Delivery Optimization | **Never** (pinned; Microsoft says do not intercept) | L0 metadata |
| QUIC / raw TCP / pinned apps | No | L0 metadata |

## 7. What an outbound request trace contains

Always (every layer):

- the caller: process image and PID, or container name when known
- the remote IP and port, and the hostname (DNS/SNI/CONNECT)
- start time, duration, bytes out and in
- the capture layer that produced it

When it came through L1 or L2, also:

- method, full URL, HTTP version, status, error
- request and response headers, redacted
- request and response bodies: capped, redacted, with content type
- TLS details as seen by the proxy
- the trace ID, so an inbound request that caused an outbound one (Janus → GitHub) links into one request trace

Each request trace should carry a `fidelity` field (`connection` | `http`) so the origin map can show which outbound requests are only partly visible.

## Open decisions for the admin

1. Accept an interception CA at all? If so, scope it to opted-in containers and processes only, or also the Windows machine Root store? (Recommend containers and processes only.)
2. Is a privileged eBPF collector in the Docker Desktop VM acceptable, given it depends on ECI being off and is unsupported?
3. The redaction and retention policy for captured bodies containing secrets.
