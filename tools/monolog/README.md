# Monolog

`monolog` is the first shared logging utility for the monorepo. It is a small,
standard-library-only Go module that writes one structured event per JSONL line.
Use it directly in Go tools and treat its event envelope as the mapping target
for Rust, TypeScript, JavaScript, and Zig applications using their native
logging APIs.

## Event format

```json
{"time":"2026-10-04T12:00:00Z","level":"info","service":"meingrad","message":"project task started","fields":{"project":"janus","task":"build"}}
```

`time`, `level`, `service`, and `message` are required. `trace_id` and
`request_id` are optional correlation fields. `fields` holds small operational
metadata such as project, task, duration, or an error code. Keep event names and
field names stable so logs from different projects can be queried together.

## Go usage

```go
logger := monolog.Default("meingrad")
_ = logger.Info("project task started", map[string]any{
    "project": "janus",
    "task":    "build",
})
```

Use `monolog.New(writer, service)` when the caller owns the output stream. Each
logger serializes concurrent writes to preserve one complete JSON object per
line. The logger deliberately does not open files, rotate logs, ship telemetry,
or impose a process-wide global logger.

## Privacy and adoption

Do not place credentials, tokens, request or response bodies, prompts, user
content, or secret-bearing values in `message` or `fields`. This package does
not guess which arbitrary field values are sensitive, so callers must only add
approved operational metadata. In particular, Personal Memory's audit trace
redaction is domain-specific and should remain in place when mapping those
events.

Janus Rust already uses `tracing` and has structured diagnostics and request
correlation. A future adapter can map tracing metadata into this envelope
without changing its deterministic core. Other runtimes should follow the same
JSONL field names; importing this Go module from those projects is not needed.
