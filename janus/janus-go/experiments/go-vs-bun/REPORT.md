# Go (net/http + nats.go) vs Bun (Elysia + nats.js)

Two gateways with the same four routes, driven by one Go load generator over 64 keep-alive
connections for 8 s after a 2 s warm-up. Both talk to the same NATS server (`hub`), which answers
`bench.echo`. Run it with `pwsh run.ps1`. Windows 11, 12 logical cores, Go 1.27.1, Bun 1.4.2,
Elysia 1.4.30, nats.js 2.29.3. The load generator, hub and server share the machine.

| route | what it does |
|---|---|
| `/ping` | returns `{"ok":true}` |
| `/validate` | parses a JSON body and validates it (struct checks in Go, `t.Object` in Elysia) |
| `/nats` | one NATS request/reply per HTTP request, the gateway pattern |
| `/cpu` | recursive `fib(27)`, CPU-bound |

## Results (two runs, requests per second; p50 / p99 in ms)

| route | Go run 1 | Go run 2 | Bun run 1 | Bun run 2 | Go : Bun |
|---|---|---|---|---|---|
| ping | 166,933 (0.36 / 2.01) | 195,801 (– / 1.80) | 77,037 (0.71 / 5.19) | 100,492 (0.51 / 5.57) | ~2x |
| validate | 124,866 (0.52 / 2.38) | 132,349 (0.52 / 2.25) | 51,846 (1.11 / 4.42) | 71,651 (0.91 / 5.55) | ~2x |
| nats | 94,794 (0.57 / 1.73) | 98,311 (0.55 / 1.65) | 29,129 (2.00 / 10.23) | 32,717 (1.58 / 9.48) | ~3x |
| cpu | 9,873 (5.98 / 16.81) | 10,456 (5.50 / 22.41) | 1,122 (57.2 / 60.7) | 982 (64.4 / 73.5) | ~10x |

Zero errors in every run. Memory (working set): Go idles near 9 MB and sits at 20 MB (run 2) to
58 MB (run 1) under load; Bun idles near 44 MB and reaches 60 to 112 MB under load, highest on
the NATS route. Startup to first accepted connection, including the script's own polling:
Go 340 to 870 ms, Bun 570 to 1,575 ms. Treat startup as a rough figure.

Binaries: `goserver.exe` is 11 MB.

## Reading it

- Plain HTTP and JSON: Go is about 2x ahead, and both are far above anything Janus will see.
- HTTP to NATS to HTTP: about 3x, with a 10 ms p99 on Bun against 1.7 ms on Go. nats.js costs more
  per message than nats.go.
- CPU-bound handlers: about 10x, because Go spreads the work over all cores and Bun runs one JS
  thread. The Bun server is one process; running one per core would narrow this.
- The 64-connection queue explains the large Bun cpu latency: one core, 64 waiting requests.

## Caveats

- Single machine, so the load generator competes for CPU with the server under test. Absolute
  numbers are not meaningful. The ratios were stable across two runs.
- Run-to-run spread is about 20% (Go ping 167k then 196k). `p50=0.00ms` in one cell is timer
  resolution on Windows.
- Memory is sampled once after each scenario, not tracked continuously.
- Only a stateless gateway is compared. Nothing here covers Turso, embedded NATS, Tailscale or Wasmtime.
