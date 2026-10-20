# ENG-82: Capturing full inbound request traces at Caddy

Linear: ENG-82 (parent map ENG-80, project Origin Admin). Terms follow `portfolio/admin/GLOSSARY.md`: a **request trace** is the full record of one **inbound request** (Cloudflare -> Caddy -> **upstream**), with each hop's timing, headers and bodies. The admin server is the only Turso writer, so every request trace has to end up there.

Researched 2026-10-19 against Caddy **v2.11.7**. That is the version in the running `caddy:2.11` image: `caddy version` in `steven3zx-edge-caddy-1` prints `v2.11.7`. `caddy list-modules` shows only the 135 standard modules, including `http.handlers.log_append`, `http.handlers.tracing`, `caddy.logging.writers.net` and `caddy.logging.encoders.filter.multi_regexp`. The experiments ran in a throwaway `caddy:2.11` container (`eng82-caddy-exp`, since removed). The edge stack and the `edge/` files were not touched.

## Answer in brief

- **Recommended:** a small **custom Caddy handler module** (built with xcaddy into a custom image, as `caddy:2.11-builder` documents). It tees the request and response bodies into buffers capped at 1 MB, without changing what reaches the upstream or the client. It redacts the capture and adds it to the **access log entry** Caddy already writes. The access log already has full headers, timing, `traceID` and the upstream (via `log_append`). The log goes to the admin server either as a push over Caddy's built-in `net` log writer or as a pull from a shared volume that the admin server tails. One event per request, one pipeline, no extra proxy hop.
- **Interim option with no custom image:** use stock `log` + `log_append` (headers, upstream, timings, `traceID`) and capture bodies only on matched routes such as `/admin/*`. Stock body capture has no size cap, buffers the whole response (which breaks streaming), and needs `request_buffers` to avoid breaking request bodies (see the finding below).
- **`tracing` (OpenTelemetry)** is worth turning on for the `traceID` and for `traceparent` propagation. It cannot carry bodies, and getting OTLP into the admin server means running a receiver there, so it supplements the access log and does not replace it.
- **Rejected:** a capture proxy in front of each upstream (five or more extra hops to run, and it duplicates what Caddy already knows) and mirroring. Caddy has no tee or mirror, and `copy_response` is not one.

## Surprising findings

1. **`log_append ... {http.request.body}` broke proxied POSTs in 2.11.7.** With it placed before a plain `reverse_proxy`, a form POST came back as `502` and the error log said `ContentLength=28 with Body length 0`. Adding `request_buffers 1MB` inside `reverse_proxy` fixed it (`200`, body intact). The placeholder reads the whole body with `io.Copy` into a `bytes.Buffer`, with no cap, and then replaces `req.Body` (`modules/caddyhttp/replacer.go`, `readRequestBodyForPlaceholder`). The placeholder field was also written twice into the entry (two `"req_body"` keys).
2. **`{http.response.body}` buffers the whole response before sending any of it.** `log_append` builds a `NewResponseRecorder` whose should-buffer function always returns `true`, and writes to the client only after the handler returns (`modules/caddyhttp/logging/logappend.go`). There is no size cap. Server-sent events, Gitea clones and large downloads would stall or use a lot of memory, so stock response-body capture is not safe for every request.
3. **The `access log` reports the final status as written by the recorder.** In the 502 case it logged `"status":200,"size":0` while the error log said 502. Don't trust stock body capture when debugging errors.
4. **Default redaction already exists.** `Cookie`, `Set-Cookie`, `Authorization` and `Proxy-Authorization` are logged as `REDACTED` unless the `log_credentials` global option is set ([log directive docs](https://caddyserver.com/docs/caddyfile/directives/log)). The experiment confirmed this for inbound `Cookie`/`Authorization` and upstream `Set-Cookie`.
5. **There is already an OpenTelemetry collector, Tempo and Jaeger on the origin host** (`janus-otel-collector`, `janus-tempo`, `janus-jaeger` from Janus). Caddy's spans could go there for free. That is useful for cross-checking, but it does not reach Turso.

## Comparison

### 1. Structured access logs (`log`, JSON)

**What an entry includes.** `request` holds `remote_ip`, `remote_port`, `client_ip`, `proto`, `method`, `host`, `uri`, `headers` and `tls` (resumed, version, cipher suite, protocol, server name). The rest of the entry has `bytes_read`, `user_id`, `duration`, `size`, `status` and `resp_headers` ([Caddy logging docs](https://caddyserver.com/docs/logging)). A real entry from the experiment:

```json
{"logger":"http.log.access.log0","msg":"handled request",
 "request":{"client_ip":"172.17.0.1","proto":"HTTP/1.1","method":"POST","host":"127.0.0.1:18080","uri":"/login",
   "headers":{"Content-Type":["application/x-www-form-urlencoded"],"Content-Length":["28"]}},
 "bytes_read":28,"duration":0.001236047,"size":75,"status":200,
 "resp_headers":{"Via":["1.1 Caddy"],"Set-Cookie":["REDACTED"]},
 "traceID":"3835ef1e942cfd3207a916785cc46436","spanID":"c5d557007b177be6",
 "upstream":"127.0.0.1:9000","upstream_latency_ms":0.836789,"upstream_duration_ms":0.916394}
```

**Which upstream and the per-hop timing.** These are not in the entry by default, but `log_append` ([docs](https://caddyserver.com/docs/caddyfile/directives/log_append)) can add any placeholder. `reverse_proxy` sets `{http.reverse_proxy.upstream.hostport}`, `.upstream.latency` (time until the upstream wrote its response headers), `.upstream.duration` (time proxying, including writing the body to the client), `{http.reverse_proxy.duration}` (total, including upstream selection and retries) and `.retries`, each also as `_ms` (doc comment in [`reverseproxy.go`](https://github.com/caddyserver/caddy/blob/master/modules/caddyhttp/reverseproxy/reverseproxy.go)). The top-level `duration` is the whole Caddy hop. For the Cloudflare hop, the request headers contain `Cf-Ray`/`Cf-Connecting-Ip`. Without `trusted_proxies` set to Cloudflare's ranges, `client_ip` is a Cloudflare address.

**Bodies.** Yes, but only through `log_append` with `{http.request.body}`, `{http.request.body_base64}`, `{http.response.body}` or `{http.response.body_base64}`. The docs call this a debugging feature because of the performance cost. The request body is read early and buffered. The response body turns on response buffering ([log_append docs](https://caddyserver.com/docs/caddyfile/directives/log_append)). Neither has a 1 MB cap (see findings 1 and 2). `request_body max_size` rejects oversized bodies with `413` ([docs](https://caddyserver.com/docs/caddyfile/directives/request_body)). That changes behaviour instead of truncating the capture, so it can't serve as the capture cap.

**Redaction.** Credential headers are redacted by default (finding 4). The `filter` encoder adds `delete`, `replace`, `regexp`, `multi_regexp`, `hash`, `cookie`, `set_cookie`, `query`, `ip_mask` and `rename` per field ([log docs](https://caddyserver.com/docs/caddyfile/directives/log)). Login form fields can be scrubbed with `req_body multi_regexp { regexp "password=[^&]*" "password=REDACTED" }`, which the experiment confirmed. That is regex over a string, so JSON or multipart logins need their own patterns.

**Performance.** Headers-only logging is cheap. Body placeholders mean a full in-memory copy and, for responses, the loss of streaming.

**TLS.** Caddy terminates TLS, so everything logged is plaintext, along with the `tls` handshake facts.

**Delivery to the admin server.** Two ways ([log docs](https://caddyserver.com/docs/caddyfile/directives/log)):
- *Push:* `output net admin:<port> { soft_start }` streams newline-delimited JSON over TCP or UDP. While the socket is down, logs fall back to stderr. They are not queued, so events are lost while the admin server is restarting.
- *Pull:* `output file` to a volume shared with the admin container. The file rolls (default 100 MiB, 10 files kept) and the admin server tails it with a saved offset. This survives admin restarts.

**Maintenance.** Stock image, configured only in the Caddyfile.

### 2. `tracing` directive (OpenTelemetry)

**Spans and attributes.** The tracing handler wraps the rest of the route in `otelhttp.NewHandler`, so each request gets **one server span**. The name defaults to `handler` or is set with `span <name>`. It carries the default HTTP semantic-convention attributes plus any `span_attributes` (placeholders are allowed and are evaluated after the downstream handler returns). The resource has `webengine.name`/`webengine.version` but no `service.name` ([directive docs](https://caddyserver.com/docs/caddyfile/directives/tracing), [`tracer.go`](https://github.com/caddyserver/caddy/blob/master/modules/caddyhttp/tracing/tracer.go)). `tracer.go` only wraps the handler, and nothing in it adds a client span for the upstream round trip. Upstream timing still has to come from the `reverse_proxy` placeholders.

**`traceparent` propagation.** An incoming W3C trace context is kept. Otherwise a new trace starts. Caddy then injects its own span context into the request headers before the next handler runs (`ot.propagators.Inject(ctx, propagation.HeaderCarrier(r.Header))`), so the upstream receives `traceparent` with the same trace ID and Caddy's span as parent. The experiment confirmed this: sending `00-0af7…319c-b7ad…3331-01` made the upstream see `00-0af7…319c-925d08dd6e9d22b7-01`. Propagators come from `autoprop` (tracecontext and baggage by default, configurable with `OTEL_PROPAGATORS`). Caddy also sets `{http.vars.trace_id}`/`{http.vars.span_id}` and adds `traceID`/`spanID` to the access log entry. That ties a request trace to any spans the upstream emits.

**Export.** Configured only through `OTEL_*` environment variables. The exporter comes from `autoexport.NewSpanExporter`, so `OTEL_TRACES_EXPORTER` (`otlp`, `console`, `none`) and `OTEL_EXPORTER_OTLP_PROTOCOL`/`_ENDPOINT`/`_HEADERS` apply. The docs describe gRPC. Spans go out batched (`sdktrace.WithBatcher`).

**Bodies and headers.** No bodies. Specific headers can be copied in through `span_attributes` placeholders, but not "all headers".

**Redaction, performance, maintenance.** Nothing sensitive is captured by default. The cost is low (batched export). Stock image. To reach Turso, the admin server would have to host an OTLP receiver, or the existing `janus-otel-collector` would have to forward to it. That is much more machinery than reading the access log.

### 3. Custom module built with xcaddy (recommended)

**Shape.** An `http.handlers.capture` middleware ordered just before `reverse_proxy` in each site:
- **Request:** wrap `r.Body` in a reader that passes every byte through to the upstream and copies the first 1 MB into a buffer, counting the total and setting `truncated`. Streaming is unchanged and no `request_buffers` is needed.
- **Response:** wrap `w` in a `caddyhttp.ResponseWriterWrapper` that writes straight through and copies the first 1 MB. The wrapper's `Unwrap()` lets `http.ResponseController` still reach `Flush`/`Hijack` ([`responsewriter.go`](https://github.com/caddyserver/caddy/blob/master/modules/caddyhttp/responsewriter.go)), so SSE and WebSocket upgrades keep working. For a hijacked (101) connection, record the upgrade and skip the frames.
- **After `next.ServeHTTP` returns:** redact, then attach the result to the access log entry the same way `log_append` does (`logappend.go` adds fields to the request's extra log fields). Headers, timings, upstream and `traceID` then all come from the stock entry, and the module adds only `req_body`/`resp_body`, their sizes and the truncation flags.

**Body capture and large bodies.** A hard 1 MB cap in each direction, true streaming, and memory bounded at 2 MB per request in flight. Content types like `video/*`, `application/octet-stream` and git packfiles can be skipped, recording only the size.

**Redaction hooks.** Done in Go before the event leaves Caddy: drop the `password`, `totp`/authenticator-code and `csrf` fields from `application/x-www-form-urlencoded` and JSON bodies, and keep Caddy's header redaction on. The log `filter` encoder still works on top as a second layer.

**Performance.** Two bounded copies per request. Nothing else is held back, unlike stock `{http.response.body}`.

**TLS.** Same as the access log: plaintext inside Caddy.

**Delivery.** Same as the access log: `net` push or a shared file that the admin server tails.

**Maintenance.** A custom image. `FROM caddy:2.11-builder AS builder` → `xcaddy build --with <module>` → `FROM caddy:2.11` + `COPY --from=builder /usr/bin/caddy /usr/bin/caddy` ([build docs](https://caddyserver.com/docs/build)). The module lives in this repo, roughly 200 lines of Go plus tests, and must be rebuilt on every Caddy bump. The module API it uses (`caddyhttp.MiddlewareHandler`, `ResponseWriterWrapper`) has been stable across 2.x.

### 4. Capture proxy between Caddy and each upstream

A small Go reverse proxy (or mitmproxy in reverse mode) for each upstream: portfolio, admin, janus:9091, grafana:3000, gitea:30001, plane:8080.
- **Bodies:** full control over capture and the cap. **Redaction:** in the proxy. **TLS:** none needed, since Caddy already sends plaintext to upstreams.
- **Downsides:** an extra hop and process for every upstream, more latency and more ways to fail. It can't see Caddy's own timing, TLS facts or which `handle` matched, so it has to be stitched to Caddy's log through `traceparent`. Every new site needs another proxy. **Delivery:** the proxy pushes to the admin server.
- **Maintenance:** stock Caddy, but a custom proxy image to run per upstream. It costs more than the module and gives a worse picture.

### 5. Mirroring and teeing in other ways

- **`copy_response`/`copy_response_headers`** only work inside `handle_response`. They copy the upstream's response to the client as a replacement for the default response ([reverse_proxy docs](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)). They don't duplicate traffic anywhere.
- **`request_body`** only offers `max_size` (413 when exceeded) and the experimental `set`. It has no capture or tee.
- **No built-in mirror or tee.** The `reverse_proxy` docs describe none, and the standard module list has none.
- **Packet-level capture** (tcpdump on the compose network): plaintext between Caddy and the upstreams is visible, but reassembling HTTP/1.1 and h2c streams, redacting and stitching to timings is all custom work. Not worth it.

## Recommendation and trade-offs

1. **Now, stock image:** in each site block add a shared snippet with `log { output net admin:<port> { soft_start } format filter { wrap json ... } }`, `log_append` for upstream, latency and duration, and `tracing { span <site> }` for `traceID` and `traceparent`. Headers are redacted by default. No bodies yet, except an optional `/admin/*`-only `log_append` of the request body with `request_buffers 1MB` (login is short form traffic).
2. **Next:** replace body capture with the custom `capture` module in a custom image (option 3). This is the only option that meets the 1 MB cap, keeps streaming and doesn't add a hop.
3. **Admin server side:** a capture listener on the compose network only, never routed through Caddy. It parses each line into a request trace, redacts again, writes to Turso and folds the requests into the next **frame**. If losing events while the admin server restarts matters, use the shared-volume file and tail it instead of `net`.

Trade-offs accepted: a custom Caddy image to maintain, and bodies kept only up to 1 MB in each direction. In exchange: one event per request, built mostly from fields Caddy already produces.

## Sketch of the event shape

One request trace, as the access log line (with module fields) maps to it:

```jsonc
{
  "trace_id": "0af7651916cd43dd8448eb211c80319c",   // traceID (tracing directive)
  "span_id": "925d08dd6e9d22b7",
  "ts": "2026-10-19T23:58:59.976Z",                 // ts
  "site": "www.steven3zx.com",                       // request.host
  "client": { "ip": "203.0.113.7", "cf_ray": "8c1f…-AMS", "remote_ip": "172.68.x.x" },
  "request": {
    "method": "POST", "uri": "/admin/api/login", "proto": "HTTP/2.0",
    "tls": { "version": 772, "cipher_suite": 4865, "server_name": "www.steven3zx.com" },
    "headers": { "Cookie": ["REDACTED"], "Content-Type": ["application/json"] },
    "body": "{\"admin\":\"steven\",\"password\":\"REDACTED\",\"code\":\"REDACTED\"}",
    "body_bytes": 64, "body_truncated": false
  },
  "upstream": { "hostport": "admin:8091", "latency_ms": 3.1, "duration_ms": 3.4, "retries": 0 },
  "response": {
    "status": 200,
    "headers": { "Set-Cookie": ["REDACTED"], "Content-Type": ["application/json"] },
    "body": "{\"ok\":true}", "body_bytes": 11, "body_truncated": false
  },
  "timing": { "caddy_total_ms": 4.0, "proxy_total_ms": 3.6 }  // duration, http.reverse_proxy.duration_ms
}
```

`trace_id` is the join key with any spans the upstream emits, because it arrives in `traceparent`. The Cloudflare hop's own timing isn't visible from the origin host. Only `Cf-Ray` ties the request to Cloudflare's logs.
