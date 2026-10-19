# Headscale and DERP hosting options and cost

Research for Linear ENG-75 (parent ENG-72), related to ENG-59. Checked on 2026-10-19. Facts and sources only; no recommendation. Headscale facts are for **v0.29.5** (latest release, published 2026-10-13, [GitHub release](https://github.com/juanfont/headscale/releases/tag/v0.29.5)). "Docs" means headscale.net/stable; "source" means the v0.29.5 tag of [juanfont/headscale](https://github.com/juanfont/headscale/tree/v0.29.5).

## 1. Supported deployment shapes

- Single tailnet only. The [FAQ](https://headscale.net/stable/about/faq/) says Headscale "implements a narrow scope, a single Tailscale network (tailnet)", aimed at personal use or small open-source organisations. Per-account separation inside Headscale is therefore by tags and ACLs within one tailnet.
- Install methods per the FAQ: official binaries, DEB packages, community and distro packages. Container images are built but the project "does not officially support" Docker deployment.
- Requires "a reasonably modern Linux or BSD based operating system", a dedicated local user, and a server with a public IP (dual-stack recommended) ([requirements](https://headscale.net/stable/setup/requirements/)).
- No high availability or clustering is documented (the FAQ and requirements pages do not cover it). One process owns the state.
- Scale: no firm number. The FAQ says "hundreds of devices" is likely fine when the network changes little; many frequent node changes make it struggle because each change recalculates the whole network map. Performance is not a primary project focus ([FAQ](https://headscale.net/stable/about/faq/)).
- Database: SQLite is the default and recommended. In the [v0.29.5 config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml), Postgres is "highly discouraged as it is only supported for legacy reasons"; new development and testing is SQLite-first (the FAQ calls Postgres "maintenance mode").

## 2. Minimum host size

- Headscale publishes **no CPU, RAM or disk minimum** ([requirements](https://headscale.net/stable/setup/requirements/) lists no hardware specifications). Section 9 lists provider plans only as price points.
- Ports ([requirements](https://headscale.net/stable/setup/requirements/)): tcp/443 public (also needed for embedded DERP); tcp/80 public only for the built-in Let's Encrypt HTTP-01 challenge; udp/3478 public STUN, needed if embedded DERP is enabled; tcp/50443 public only for remote gRPC control; tcp/9090 metrics, not public.

## 3. State storage and backup

- Default paths: config `/etc/headscale/config.yaml`; data directory `/var/lib/headscale` holding the SQLite DB (`db.sqlite`), noise key and DERP key ([requirements](https://headscale.net/stable/setup/requirements/), [config-example](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)).
- SQLite WAL is on in the example config (`write_ahead_log: true`, `wal_autocheckpoint: 1000`), which the config calls recommended for production.
- Key files: `noise.private_key_path` (`noise_private.key`) and `derp.server.private_key_path` (`derp_server_private.key`); "a missing key will be automatically generated" ([config-example](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)).
- Official backup guidance is limited to upgrades: stop Headscale, copy `/etc/headscale` and `/var/lib/headscale`; for Postgres, use Postgres's own tooling ([upgrade guide](https://headscale.net/stable/setup/upgrade/)). The docs fetched do not mention Litestream or continuous replication.
- Upgrades must go one stable minor version at a time (for example 0.26.0, 0.27.1, 0.28.0), using the latest patch of each; migrations run automatically ([upgrade guide](https://headscale.net/stable/setup/upgrade/)).
- Policy (ACL) is loaded from a file at `policy.path` and reloaded on SIGHUP, or optionally stored in the database ([policy docs](https://headscale.net/stable/ref/policy/)).

## 4. How the control plane joins the tailnet as a node

- Headscale itself is not a tailnet node, and its docs do not describe a control plane joining. The documented client method is `tailscale up --login-server <URL> --authkey <KEY>` ([registration docs](https://headscale.net/stable/ref/registration/)).
- For an in-process Go node, the Tailscale `tsnet` library embeds a node. `tsnet.Server` has `ControlURL` (falls back to env `TS_CONTROL_URL`, then Tailscale's default), `AuthKey`, `Ephemeral`, `AdvertiseTags`, `Hostname`, `Dir`, `Store`, `Port` ([pkg.go.dev/tailscale.com/tsnet](https://pkg.go.dev/tailscale.com/tsnet), module v1.104.1 published 2026-10-07). `AuthKey` is ignored if the node already exists in stored state. `AdvertiseTags` may be refused by the control server. Default state file: `Dir/tailscaled.state`.
- The [tsnet KB page](https://tailscale.com/kb/1244/tsnet) does not mention `ControlURL` or third-party control servers; the field comes from the API reference above. tsnet uses a userspace TCP/IP stack.
- Not verified: running tsnet against Headscale from a Janus process was not tested here.

## 5. Pre-auth keys and `tag:acct-<id>` tags through the API

- REST base `/api/v1`, Bearer auth `Authorization: Bearer <API_KEY>`, schema at `/swagger` ([API docs](https://headscale.net/stable/ref/api/)). API keys: `headscale apikeys create` (default expiry 90 days, shown once), `list`, `expire --prefix`.
- Routes from [headscale.proto](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/proto/headscale/v1/headscale.proto): `POST /api/v1/preauthkey`, `POST /api/v1/preauthkey/expire`, `DELETE /api/v1/preauthkey`, `GET /api/v1/preauthkey`.
- `CreatePreAuthKeyRequest` fields ([preauthkey.proto](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/proto/headscale/v1/preauthkey.proto)): `uint64 user`, `bool reusable`, `bool ephemeral`, `Timestamp expiration`, `repeated string acl_tags`. The exact JSON casing should be confirmed against `/swagger` on the pinned build (already noted in `janus/research/platform-fact-checks.md`).
- Behavior in source ([grpcv1.go](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/hscontrol/grpcv1.go)): each tag is checked by `validateTag` (invalid gives InvalidArgument); `user` is optional (0 means no user, so a tags-only key). The create handler does not check `tagOwners` policy.
- CLI equivalent: `headscale preauthkeys create --tags tag:<TAG>`; at least one tag is required for tagged nodes; defaults are single-use and valid one hour ([registration docs](https://headscale.net/stable/ref/registration/)). Tags come from the key, so the client must not pass `--advertise-tags`.
- Ownership: tagged nodes are owned by the special user `tagged-devices` ([registration docs](https://headscale.net/stable/ref/registration/), [tags docs](https://headscale.net/stable/ref/tags/)). Tagged nodes are exempt from default key expiry ([config-example](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)).
- Tags can also be set later: `headscale nodes tag -i <ID> -t tag:a,tag:b`; the tags page says admins can use CLI or API ([tags docs](https://headscale.net/stable/ref/tags/)).
- Ephemeral nodes are deleted after `node.ephemeral.inactivity_timeout` (default 30m) ([config-example](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)).
- A Go client library exists but is community-maintained, not by the Headscale authors: [headscale-client-go](https://github.com/hibare/headscale-client-go) ([integration tools](https://headscale.net/stable/ref/integration/tools/)).
- gRPC listens on `grpc_listen_addr` (default 127.0.0.1:50443); remote use needs valid TLS or `grpc_allow_insecure` ([config-example](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)). The policy page documents no tag requirement for pre-auth keys ([policy docs](https://headscale.net/stable/ref/policy/)).

## 6. DERP relay requirements

Embedded (inside Headscale) ([DERP docs](https://headscale.net/stable/ref/derp/), [config-example](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)):
- `derp.server.enabled: false` by default. `server_url` "MUST be using https, DERP requires TLS". `stun_listen_addr` MUST be set (default `0.0.0.0:3478`, UDP).
- Region defaults: `region_id: 999`, code `headscale`. Optional `ipv4`/`ipv6` published in the DERP map for connection stability. `verify_clients: true` by default (only tailnet nodes may relay).
- Ports needed: tcp/443 and udp/3478 on the Headscale host ([requirements](https://headscale.net/stable/setup/requirements/)).
- `derp.urls` defaults to Tailscale's public DERP map; setting it to `[]` removes those and leaves only your server (docs warn this is a single point of failure). Limitation: the embedded server does not serve `/generate_204` on tcp/80 (no captive-portal checks).

Separate `derper` ([Tailscale custom DERP](https://tailscale.com/kb/1118/custom-derp-servers), [Headscale DERP docs](https://headscale.net/stable/ref/derp/)):
- Tailscale: ports 80, 443 and 3478 (STUN) reachable from the internet; a DNS name pointing at the server, started with `--hostname`; direct internet access, not behind NAT, a firewall or a load balancer (load balancers often lack HTTP-upgrade support); ICMP in and out allowed; IPv4/IPv6 in the DERP map optional but recommended and must be public; install via `go install tailscale.com/cmd/derper@latest` and rebuild periodically; region IDs 900-999 are reserved for custom regions; no bandwidth or sizing figures. That page does not say whether STUN is UDP or TCP; Headscale's config uses UDP.
- Headscale side: define the region in a `derp.paths` YAML (`regions` with `regionid`, `regioncode`, `regionname`, `nodes` with `name`, `hostname`, `ipv4`, `ipv6`, `canport80`) and set `derp.server.enabled: false` if only external servers are used. Client gating: run derper with `-verify-client-url https://<headscale>/verify`; `-verify-client-url-fail-open` (default allow) controls behavior when Headscale is unreachable. Tailscale's page instead describes `--verify-clients` with `tailscaled` on the same host.
- TLS for Headscale itself: built-in Let's Encrypt (HTTP-01 on port 80 or TLS-ALPN-01 on the listen port; renewal attempted when 30 days or fewer remain), your own cert files, or a reverse proxy ([TLS docs](https://headscale.net/stable/ref/tls/)). HTTPS on 443 is "strongly recommended" because clients assume it in some cases ([requirements](https://headscale.net/stable/setup/requirements/)).

## 7. Outbound-only agents

Not stated in a primary source in this form. What the sources say: clients reach the control server over HTTPS (tcp/443, TS2021 Noise protocol per the `noise:` config), and DERP relays are reached over HTTPS on 443 with STUN on UDP 3478 for NAT discovery ([requirements](https://headscale.net/stable/setup/requirements/), [config-example](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml), [Tailscale DERP page](https://tailscale.com/kb/1118/custom-derp-servers)). Whether a given customer network permits that was not tested.

## 8. Tailscale client versions an agent can embed

- Policy: Headscale aims to support "the last 10 releases" of the Tailscale client ([clients docs](https://headscale.net/stable/about/clients/)); the code constant is `SupportedMajorMinorVersions = 10`.
- In v0.29.5, `MinSupportedCapabilityVersion = 113`, which the generated map ties to **Tailscale v1.80** ([capver_generated.go](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/hscontrol/capver/capver_generated.go)). Other mappings: v1.90=130, v1.92=131, v1.96=133, v1.98=138. `capver.go` notes v1.104 is the first release at or above capability version 148.
- Headscale v0.29.5 builds against `tailscale.com v1.101.0-pre` ([go.mod](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/go.mod)). Latest Tailscale release is v1.104.1 (2026-10-07, [GitHub](https://github.com/tailscale/tailscale/releases/tag/v1.104.1)); the `tsnet` module v1.104.1 is on pkg.go.dev.
- The v0.29.5 release notes list no client-version change ([release](https://github.com/juanfont/headscale/releases/tag/v0.29.5)). The clients page does not mention tsnet or custom or embedded clients; it names only the official clients per OS. So embedded `tsnet` against Headscale has no explicit support statement in the docs.

## 9. Rough monthly cost (single small VM)

A VM running Headscale with embedded DERP needs a public IPv4 with udp/3478 and tcp/443 open. DERP relays traffic, so egress is the main variable cost.

| Provider | Plan | Monthly | Included outbound | Overage | Source |
|---|---|---|---|---|---|
| DigitalOcean Droplet | 512 MiB / 1 vCPU / 10 GiB | $4.00 | 500 GiB | $0.01/GiB | [pricing](https://www.digitalocean.com/pricing/droplets), [bandwidth](https://docs.digitalocean.com/platform/billing/bandwidth/) |
| DigitalOcean Droplet | 1 GiB / 1 vCPU / 25 GiB | $6.00 | 1,000 GiB | $0.01/GiB | same |
| DigitalOcean Droplet | 2 GiB / 1 vCPU / 50 GiB | $12.00 | 2,000 GiB | $0.01/GiB | same |
| Fly.io shared-cpu-1x | 256 MB / 512 MB / 1 GB | $2.19 / $3.69 / $6.70 | none | $0.02/GB NA and EU, $0.04/GB APAC, Oceania, S. America, $0.12/GB Africa and India | [pricing](https://docs.fly.io/about/pricing/) |
| Fly.io dedicated IPv4 | per app | $2.00 | n/a | n/a | same |
| Hetzner Cloud CX23 (2 vCPU / 4 GB / 40 GB), EU | | EUR 5.99 incl. IPv4, excl. VAT | 20 TB | not found | third-party only, see below |
| Hetzner Cloud CPX22, EU | | EUR 19.99 incl. IPv4, excl. VAT | 20 TB | not found | third-party only |

Notes:
- DigitalOcean allowance is pooled per team, inbound is free, and IPv6 counts toward the pool ([bandwidth docs](https://docs.digitalocean.com/platform/billing/bandwidth/)).
- Fly: UDP needs a dedicated IPv4; shared IPv4 and public IPv6 carry TCP only; the UDP socket must bind to `fly-global-services` and the port is not rewritten; plan around roughly 1300-byte packets ([Fly UDP docs](https://docs.fly.io/networking/udp-and-tcp)). Volumes are $0.15/GB-month; inbound traffic is free ([pricing](https://docs.fly.io/about/pricing/)).
- Hetzner: Hetzner's own page fetched here showed blank price fields and listed plans as unavailable ([Hetzner](https://www.hetzner.com/cloud/regular-performance)). The euro figures come from third-party pages found by search ([Kimmo Suominen comparison 2026-08](https://kimmo.suominen.com/stuff/cpc-2026-08.txt), [Findstack](https://findstack.com/resources/hetzner-price-increase-2026)), which also report an IPv4 charge of EUR 0.50/month and a June 2026 price increase. Included traffic depends on region (EU 20 TB; US plans 1-5 TB; Singapore 0.5-5 TB). Treat these as unconfirmed.
- Derived arithmetic, not a quote: 1 TB (1,024 GiB) of monthly egress on the $4 DigitalOcean plan is 524 GiB over the 500 GiB allowance, about $5.24 at $0.01/GiB; 1 TB on Fly in NA or EU is about $20.48 at $0.02/GB, plus the machine price and $2 IPv4.
- Domain, backup storage and VAT are excluded. Let's Encrypt certificates are free.

## 10. Not found in primary sources

- Any Headscale hardware sizing or HA guidance.
- Official backup tooling beyond file copy.
- Exact JSON field casing for the REST pre-auth call (read `/swagger` from the pinned build).
- Hetzner's own price list (page fields were blank).
- Documentation of tsnet embedding with Headscale.
