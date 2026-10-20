# ENG-84: How deep a request trace can follow into each upstream

Research for the Origin Admin map (ENG-80). Terms follow `portfolio/admin/GLOSSARY.md`: **request trace**, **inbound request**, **outbound request**, **upstream**, **host agent**, **log line**, **audit event**.

Sources: Caddy, OpenTelemetry, Gitea and Grafana docs (linked inline), plus the code and running state of the **origin host** as of 2026-10-19. All repo reads were read-only.

## Answer in brief

- **Caddy can be the root of every request trace.** The `tracing` directive continues an incoming W3C trace context or starts a new one, and writes `traceparent` into the request before `reverse_proxy` forwards it. Any upstream that extracts W3C trace context joins the same trace.
- **Depth by upstream:** `admin` and `portfolio` can go deep with a few lines of `otelhttp`/`otelsql`. Janus is instrumented but **drops the incoming trace today** (bug below). Grafana joins the trace with two env vars. Gitea and Plane stop at the Caddy span, though Gitea's access log line can carry the trace ID.
- **Ingest path:** the admin server writes its own spans to Turso in-process with a custom `SpanExporter`, the same pattern decided for audit events. It also exposes a private OTLP/HTTP `/v1/traces` receiver that Caddy and `portfolio` export to directly, and that `janus-otel-collector` forwards Janus and Grafana spans to. The admin server stays the only writer.

## 1. Does Caddy's `tracing` start a trace and propagate `traceparent`?

Yes.

- The docs say the directive "will propagate an existing trace context or initialize a new one", using the W3C `tracecontext` and `baggage` propagators ([Caddy `tracing`](https://caddyserver.com/docs/caddyfile/directives/tracing)).
- In the source, `serveHTTP` calls `ot.propagators.Inject(ctx, propagation.HeaderCarrier(r.Header))` on the request ([caddy/modules/caddyhttp/tracing/tracer.go](https://github.com/caddyserver/caddy/blob/master/modules/caddyhttp/tracing/tracer.go)). `reverse_proxy` forwards request headers, so each upstream receives a `traceparent` whose parent is Caddy's server span.
- `tracing` is **first** in the directive order ([Caddy directive order](https://caddyserver.com/docs/caddyfile/directives)), so it wraps everything else in the site, including `reverse_proxy`.
- Caddy creates **one server span per request** (default name `handler`, settable with `span`). There is no separate client span for the upstream hop, so the Caddy span's duration includes the upstream's time.
- The exporter comes from `autoexport.NewSpanExporter` and the propagators from `autoprop` (same source file). It is configured only through standard `OTEL_*` environment variables. The `autoexport` default protocol is `http/protobuf` ([autoexport](https://pkg.go.dev/go.opentelemetry.io/contrib/exporters/autoexport)), although the Caddy docs page still says gRPC. **Set `OTEL_EXPORTER_OTLP_PROTOCOL` explicitly** so the docs and code can't disagree.
- Caddy exposes `{http.vars.trace_id}` and `{http.vars.span_id}` for use in headers, and adds `traceID`/`spanID` to its access log ([Caddy `tracing`](https://caddyserver.com/docs/caddyfile/directives/tracing)). This lets every Caddy **log line** join its request trace.

Two caveats for the edge:

1. **Callers can choose the trace.** Because Caddy continues an incoming context, an outside client can send its own `traceparent`. With the SDK's default parent-based sampler, a client that sends flags `00` (not sampled) would stop that request from being traced. Set `OTEL_TRACES_SAMPLER=always_on` on the Caddy container so every **inbound request** is recorded regardless of what the caller sent. Trace IDs from outside should be treated as untrusted labels.
2. **Spans hold no bodies.** OTel HTTP spans carry method, route, status and the headers you choose to add, not request or response bodies. The ENG-80 requirement for full headers and bodies (redacted, capped at 1 MB) needs its own capture point. That belongs to the request-trace data-layout work, not this ticket.

Edge config (sketch, not applied):

```caddyfile
# edge/Caddyfile: inside each site block (or a snippet imported by each)
tracing {
	span {method} {host}{path}
}
log_append upstream_host {rp.upstream.host}
log_append upstream_latency_ms {rp.upstream.latency_ms}
log_append upstream_duration_ms {rp.upstream.duration_ms}
```

```yaml
# edge/compose.yaml, caddy service
environment:
  OTEL_SERVICE_NAME: caddy
  OTEL_EXPORTER_OTLP_PROTOCOL: http/protobuf
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: http://admin:4318/v1/traces
  OTEL_EXPORTER_OTLP_HEADERS: Authorization=Bearer ${OTLP_INGEST_TOKEN}
  OTEL_TRACES_SAMPLER: always_on
```

`log_append` fields are added on the way back up the middleware chain by default, so they see the finished upstream response ([Caddy `log_append`](https://caddyserver.com/docs/caddyfile/directives/log_append)). The `rp.upstream.*` timings are what an uninstrumented upstream can still show (section 4).

## 2. What each upstream instruments today

| Upstream | How it runs now | Tracing today |
|---|---|---|
| `portfolio` (`portfolio/port-2`) | `portfolio` container, `:8090` | None. Stdlib `net/http` mux, no OTel dependency (`port-2/go.mod` has no requirements). |
| `admin` (`portfolio/admin/server`) | `admin` container, `:8091` | None. `go.mod` has only `x/crypto`, `x/term` and `tursogo`. Turso is opened with `sql.Open("turso", …)` and `SetMaxOpenConns(1)` (`store.go`). The host agent is called with a plain `http.Client` (`handlers.go`, `snapshot`). |
| Host agent (`portfolio/admin/agent`) | Native Windows, `127.0.0.1:9180` | None. |
| Janus (`janus/janus-go`) | **Not running.** No process is listening for it. | Full OTel SDK: traces, metrics and logs over OTLP/HTTP to `OTEL_EXPORTER_OTLP_ENDPOINT` (default `http://localhost:4318`) (`actors/telemetry/init.go`). The propagator is W3C TraceContext + Baggage. Spans: `http.edge` (server), `<METHOD> <route>`, `http.route.handler`, and internal spans for auth, authorization, config, actors, persistence, projects, wasm and GitHub sign-in. **Outbound** GitHub calls use plain `http.Client` (`actors/github/client.go`, `actors/routes/github.go`), so they get no client span and no `traceparent`. |
| `janus-otel-collector` | Container `otel/opentelemetry-collector-contrib:0.159.0`, `127.0.0.1:4317-4318` | Traces go to Tempo and Jaeger, logs to Loki, metrics to Prometheus (`observability/otel-collector.yaml`). |
| Gitea | Native `gitea.exe` **1.27.3**, `:30001` | No OTLP export (section 3). |
| Grafana | Container `grafana/grafana:12.4.11`, `127.0.0.1:3000` | Tracing not configured (no `GF_TRACING_*` in `janus-go/docker-compose.yml`). |
| `plane` | Not running (nothing on `:8080`) | Not applicable. |

### Janus drops the incoming trace (bug)

`actors/httpserver/actor.go` starts `http.edge` with `actortrace.StartSpan(r.Context(), …)` **before** anything extracts headers. With no span in `r.Context()`, this creates a new root. `web/app.go` then extracts `traceparent` only `if !trace.SpanFromContext(ctx).SpanContext().IsValid()`. Since `http.edge` is already valid, the extract never runs. Every Janus trace therefore starts its own root and never joins Caddy's trace.

**Fix:** extract in `serverHandler.ServeHTTP` before starting `http.edge`: `ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))`. Alternatively, wrap the handler with `otelhttp.NewHandler`.

### Surprise: `janus.steven3zx.com` serves Prometheus publicly

`edge/Caddyfile` sends `janus.steven3zx.com` (except `/grafana`) to `host.docker.internal:9091`. That host port belongs to **`janus-prometheus`** (`127.0.0.1:9091->9090`, `janus-go/docker-compose.yml`), not to Janus, whose default address is `:9090` (`janus.md`). A plain `GET https://janus.steven3zx.com/api/v1/status/buildinfo` returned Prometheus 3.6.0 build info without authentication, and Cloudflare proxies that hostname. This is outside ENG-84 and nothing was changed, but it should become its own ticket. It also means no Janus request can be traced until Janus runs and Caddy points at it.

## 3. Gitea, Grafana and Plane

**Gitea 1.27.3: no OTel export and no `traceparent` extraction.**
- The config cheat sheet has no tracing or OpenTelemetry section ([Gitea config cheat sheet](https://docs.gitea.com/administration/config-cheat-sheet)).
- Gitea has a built-in "OpenTelemetry-like shim-layer" (`gtprof`) with spans `http`, `git-run` and `database` ([PR #32973](https://github.com/go-gitea/gitea/pull/32973), [gtprof](https://pkg.go.dev/gitea.dev/modules/gtprof)). Traces stay inside Gitea: admins read them in the admin panel or a downloaded diagnosis report. An official OTel exporter is only "to be discussed" (PR #32973).
- **What we can get:** the access log takes a request ID from configurable headers. In `[log]`, `REQUEST_ID_HEADERS` sets the header list and `{{.RequestID}}` is available in `ACCESS_LOG_TEMPLATE` ([Gitea config cheat sheet](https://docs.gitea.com/administration/config-cheat-sheet)). Values are cut to 40 bytes ([access_log.go](https://git.fb1.uni-bremen.de/davrot/forgejo_backup/src/commit/53e324fd80fe08dc78f58fa4e0d98e02974cd18f/modules/context/access_log.go), Forgejo mirror of the same code). A 32-hex trace ID fits, so Caddy should send `X-Trace-Id`.

**Grafana 12.4: built-in OTel tracing, W3C by default.**
- `[tracing.opentelemetry.otlp]` takes `address` (a gRPC `host:port`, for example `localhost:4317`) and `propagation` (`w3c` by default). `[tracing.opentelemetry]` adds `sampler_type`/`sampler_param` (`const`/`1` by default) and `custom_attributes` ([Grafana configuration](https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/)).
- Every setting maps to `GF_<SECTION>_<KEY>` (same page). The Grafana spans then join Caddy's trace, which goes on to Grafana's data-source queries.

**Plane:** not running. No first-party Plane tracing documentation was found, so this is unverified. Plan for Caddy-span depth only. If Plane comes back, revisit OTel auto-instrumentation for its Python/Node containers.

## 4. What a trace shows for apps we can't instrument

For Gitea, Plane, and anything that ignores `traceparent`, the request trace still contains:

- The **Caddy server span**: start, end, total duration (including the upstream), method, host, path or route, status and peer address, with the HTTP semantic-convention attributes Caddy sets by default. You can add any request placeholder through `span_attributes` ([Caddy `tracing`](https://caddyserver.com/docs/caddyfile/directives/tracing)).
- The **Caddy access log line** with the same `traceID`, plus the `log_append` fields `upstream_host`, `upstream_latency_ms` (time to first byte from the upstream) and `upstream_duration_ms`. This splits Caddy's own time from the upstream's.
- For Gitea only, its **own access log line**, keyed by the same trace ID through `REQUEST_ID_HEADERS`. It adds Gitea's own status and timing for the request.

A failed hop, such as Plane being down, appears as a Caddy span with status 502 and an empty upstream latency.

## 5. Per-upstream table: achievable depth and steps

| Upstream | Achievable depth | Steps |
|---|---|---|
| **Caddy (edge)** | Root server span per inbound request, plus a correlated access log line with upstream timing | Add `tracing` and `log_append` to each site in `edge/Caddyfile`. Set the `OTEL_*` env vars on the `caddy` service (exporter, protocol, endpoint `http://admin:4318/v1/traces`, bearer header, `always_on` sampler). |
| **`admin`** | **Deepest.** Server span, then one span per Turso query, then a client span for the host-agent call. Audit events link to the span that caused them. | Wrap `s.routes()` in `otelhttp.NewHandler`, which extracts `traceparent` from Caddy ([otelhttp](https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp)). Give `s.agent` an `otelhttp.NewTransport`, which injects `traceparent` and records the call as a child span. Open Turso through `otelsql.Open("turso", path)` ([otelsql](https://pkg.go.dev/github.com/XSAM/otelsql)). Then filter out the span exporter's own inserts so writing spans doesn't create spans. Register a `TracerProvider` with a custom Turso `SpanExporter` (section 6). Do the trace work in the same slice as the audit-event logs SDK, so `LogRecord`s pick up `trace_id`/`span_id` from the request context. |
| **Host agent** | Its own server span under admin's client span (optional) | Wrap its mux in `otelhttp.NewHandler`. The agent is stateless and can't write Turso, so it must export OTLP to the admin receiver. That requires a loopback-published admin port such as `127.0.0.1:4319`. **Recommendation: defer.** Admin's client span already shows the agent call's timing and status. |
| **`portfolio`** | Server span under Caddy's, with route name and status | Wrap the mux in `otelhttp.NewHandler(mux, "portfolio")`. Install the SDK with the OTLP/HTTP trace exporter and set `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://admin:4318/v1/traces` in compose (same network as `admin`). The app has only static routes, so this is the whole depth. |
| **Janus** | Server span, route span, handler span and internal actor/auth spans, plus outbound GitHub client spans once added | (1) Get Janus running and point Caddy at Janus, not Prometheus. (2) Fix the extract-before-`http.edge` bug. (3) Give the GitHub `http.Client`s an `otelhttp.NewTransport`, which records **outbound requests** as spans and injects `traceparent`. (4) Add an `otlp_http/admin` exporter to the collector's `traces` pipeline (section 6). |
| **Grafana** | Grafana server span and its data-source query spans, joined to Caddy's trace | Set `GF_TRACING_OPENTELEMETRY_OTLP_ADDRESS=otel-collector:4317` on `janus-grafana`. Keep `propagation` at its `w3c` default. Spans then flow through the same collector forward as Janus. |
| **Gitea** | Caddy span only, plus Gitea's access log line keyed by trace ID | Add `request_header X-Trace-Id {http.vars.trace_id}` in the `git.steven3zx.com` site (`request_header` runs after `tracing`). In Gitea `[log]`, set `ENABLE_ACCESS_LOG = true` and `REQUEST_ID_HEADERS = X-Trace-Id`, and keep `{{.RequestID}}` in `ACCESS_LOG_TEMPLATE`. The log-line pipeline (a separate ticket) then joins on trace ID. |
| **`plane`** | Caddy span only (502 while it's down) | None now. Revisit if Plane is restarted. |

## 6. Recommended span-ingest path into Turso

**Recommendation:** the admin server is the single span sink, with two ways in, both written by the admin process:

```
admin's own spans ──(in-process custom SpanExporter)──────────────┐
caddy, portfolio ──OTLP/HTTP──> admin :4318 /v1/traces (private) ─┼─> Turso (admin is the only writer)
janus, grafana ──> janus-otel-collector ──otlp_http/admin──> ─────┘
```

1. **In-process for `admin` itself.** Implement `sdktrace.SpanExporter` (`ExportSpans`, `Shutdown`) to batch-insert into Turso, behind `sdktrace.WithBatcher`. This mirrors the ENG-80 decision that audit events are OTel `LogRecord`s sent "through a custom exporter into Turso". One `Resource` and one context then serve both, and an audit event carries the `trace_id` of the request that caused it. No network hop.
2. **A private OTLP/HTTP receiver in `admin`** for everything else. OTLP/HTTP is plain `POST /v1/traces` with `application/x-protobuf` (or `application/json`), optional `Content-Encoding: gzip`, and `200 OK` on success ([OTLP spec](https://opentelemetry.io/docs/specs/otlp/)). The handler unmarshals `ExportTraceServiceRequest` from `go.opentelemetry.io/proto/otlp`, which is already in the Go OTel dependency tree, maps spans to rows and inserts them. That's roughly a hundred lines.
   - Serve it on a **separate listener** (`:4318`) that Caddy doesn't route, reachable only on the `edge` compose network.
   - Require a bearer token. Caddy sends it through `OTEL_EXPORTER_OTLP_HEADERS`, and the collector exporter through `headers`.
   - Accept **traces only** at first. Audit events must never come in over the network. Any process that can reach the port could forge them, so they stay in-process as decided.
3. **Janus-side spans go through the existing collector.** Add an `otlp_http` exporter to the collector config, with `endpoint` as the base URL (`/v1/traces` is appended), `encoding: proto` and gzip by default ([otlphttp exporter](https://github.com/open-telemetry/opentelemetry-collector/blob/main/exporter/otlphttpexporter/README.md)). Use the component name `otlp_http`: `otlphttp` is a deprecated alias, and the config already uses `otlp_http/loki`. Put it in the `traces` pipeline next to Tempo and Jaeger. Tempo and Jaeger keep their copies, and Turso gets the copy Origin Admin reads.
   - **Open networking question:** the collector is on the `janus-go` compose network and `admin` is on `edge`. Either attach the collector to the `edge` network as an external network, or publish the admin receiver on `127.0.0.1:4319` and target `host.docker.internal:4319`. The first is cleaner. Neither was tested.

**Why not the other options:**

- *Everything through `janus-otel-collector`:* request tracing for the public site would then depend on the Janus dev observability stack being up, plus a hop across compose projects. The edge is the part that must always trace.
- *A new collector in `edge/compose.yaml`:* it adds batching, retry, and redaction or filter processors (useful for the `[redacted · N chars]` rule on any header attributes). It costs a container and config. Start without it. It's a drop-in later because senders only change an endpoint.
- *Tempo as the store with the admin server querying it:* this breaks "Turso is the only destination" and the 7-day retention ownership.

**Single-writer check:** every path ends in the admin process. Caddy, `portfolio`, Janus, Grafana and the collector only send OTLP. The host agent stays stateless. Turso's file lock (`portfolio/admin/README.md`) and `SetMaxOpenConns(1)` stay valid, but span inserts now share that one connection with sign-in and audit writes. Batch them, as `WithBatcher` and the collector's `batch` processor already do. Sizing waits on the data-layout ticket.

## Open items surfaced (not resolved here)

- `janus.steven3zx.com` publicly proxies to an unauthenticated Prometheus (section 2). Needs its own ticket.
- Bodies and full headers for request traces need a capture point beyond spans (section 1, caveat 2).
- Networking between the collector and `admin` (section 6, step 3).
- Plane tracing support is unverified.
