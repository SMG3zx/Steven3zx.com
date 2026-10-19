# Headscale and DERP hosting for Janus

Researched on 2026-10-19 for Linear ENG-75. Facts and sources only, no recommendation. All claims come from primary sources (headscale.net docs, the juanfont/headscale repo at tag v0.29.5, the tailscale/tailscale repo, Tailscale KB, provider pages). Each source was read through a fetch tool that summarises pages, so quotes are as reported by that tool. Anything not confirmed on a primary source is marked **unconfirmed**.

**Version baseline:** Headscale v0.29.5, released 13 October 2026 (the latest on the [releases page](https://github.com/juanfont/headscale/releases) on 2026-10-19; the page shows day and month only). Docs are the `stable` docs at headscale.net. Code links point at the `v0.29.5` tag.

## 1. Supported deployment shapes

| Shape | What the primary source says | Source |
|---|---|---|
| Single binary / systemd service | The requirements page assumes a system service run by a dedicated `headscale` user, config in `/etc/headscale/config.yaml`, SQLite, data in `/var/lib/headscale`, "a reasonably modern Linux or BSD based operating system". | [Requirements](https://headscale.net/stable/setup/requirements/) |
| Docker / Podman | Images `docker.io/headscale/headscale:<VERSION>` and `ghcr.io/juanfont/headscale:<VERSION>`. Example mounts `config:/etc/headscale:ro` and `lib:/var/lib/headscale`, runs `serve`, health check `headscale health`. The image is distroless, with a `-debug` variant that adds a shell. The page says it is community documentation "not actively maintained by the headscale authors". The FAQ says "we don't officially support deploying headscale using Docker". The README says "we do not support nor encourage the use of reverse proxies and container to run Headscale". | [Container install](https://headscale.net/stable/setup/install/container/), [FAQ](https://headscale.net/stable/about/faq/), [README](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/README.md) |
| Behind a reverse proxy | Documented, with configs for Apache, Caddy, Nginx, Istio and a description for Envoy. The proxy must support WebSockets (POST upgrade, `Upgrade: tailscale-control-protocol`). Cloudflare Proxy and Cloudflare Tunnel are "not supported". gRPC "may not be proxied". STUN (udp/3478) must be served publicly, not proxied. Headscale can run without TLS behind a proxy (empty `tls_cert_path` and `tls_key_path`, `server_url` set to `https://`). `True-Client-IP`, `X-Real-IP` and `X-Forwarded-For` are ignored unless the TCP peer matches `trusted_proxies`. | [Reverse proxy](https://headscale.net/stable/ref/integration/reverse-proxy/) |
| Embedded DERP | Built in, off by default (`derp.server.enabled`). See section 5. | [DERP](https://headscale.net/stable/ref/derp/) |
| Separate `derper` | Tailscale's own binary (`go install tailscale.com/cmd/derper@latest`). Headscale's DERP page mentions it only in the client-verification section. A custom DERP map can be supplied with `derp.paths` or `derp.urls`. See section 5. | [DERP](https://headscale.net/stable/ref/derp/), [Tailscale custom DERP](https://tailscale.com/kb/1118/custom-derp-servers) |

Other facts:
- Scope is "a _single_ Tailscale network (tailnet), suitable for a personal use" ([README](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/README.md)). Janus would run one tailnet for all accounts, with `tag:acct-<id>` as the isolation mechanism.
- The Tailscale client "assumes HTTPS on port 443 in certain situations"; other ports are "possible" but 443 is "strongly recommended for production" ([Requirements](https://headscale.net/stable/setup/requirements/)).
- Upgrades must go one stable minor version at a time (0.26.0 to 0.27.1 to 0.28.0); skipping is blocked as of 0.29.0 ([Upgrade](https://headscale.net/stable/setup/upgrade/), [CHANGELOG](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md)).
- High availability: not documented. **Unconfirmed** whether multiple Headscale instances can share a database. The FAQ and README describe a single server.

## 2. Host size, resource use, storage, backup

- **Minimum CPU/RAM/disk: not stated** on the requirements page ([source](https://headscale.net/stable/setup/requirements/)). **Unconfirmed**; no primary figure for memory per node.
- **Scale guidance (FAQ):** "Headscale is not enterprise software and our focus is homelabbers and self-hosters." It "can likely handle 100s of devices (maybe more), if there is little to no change happening in the network", and "will start to struggle" when many devices and frequent changes overlap. No fixed node limit ([FAQ](https://headscale.net/stable/about/faq/)). Janus's expected agent count and churn are not covered by any primary figure.
- **Database:** SQLite is the recommended database. PostgreSQL is supported but in "maintenance mode" ([FAQ](https://headscale.net/stable/about/faq/)); the config example calls Postgres supported only "for legacy reasons". SQLite path `/var/lib/headscale/db.sqlite`, `sqlite.write_ahead_log: true` ("recommended for production environments"), `wal_autocheckpoint: 1000`. Postgres keys: host, port, name, user, pass, max_open_conns, max_idle_conns, conn_max_idle_time_secs, ssl ([config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)).
- **0.27.0 database note:** a `schema.sql` file became the source of truth, and "We are only improving SQLite databases with this change - PostgreSQL databases are not affected" ([CHANGELOG](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md)).
- **Other state on disk:** `noise_private.key` (encrypts control traffic) and `derp_server_private.key` (embedded DERP), both generated if missing, under `/var/lib/headscale`. TLS cache in `/var/lib/headscale/cache` when Let's Encrypt is used ([config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)). Losing the noise key means clients must re-establish trust: **unconfirmed**.
- **Backup:** The docs give only file-copy backup of `/etc/headscale` and `/var/lib/headscale` (`cp -aR`), or the whole volume directory for containers. For Postgres they defer to PostgreSQL's own documentation. They recommend a backup before every upgrade because "Headscale applies database migrations during upgrades" ([Upgrade](https://headscale.net/stable/setup/upgrade/)). No documented online-backup or point-in-time recovery procedure for SQLite: **unconfirmed**. Copying a live WAL-mode SQLite file with `cp` is not addressed by the docs.
- **Migrations:** migration support for pre-0.25.0 databases was removed in 0.28.0, and migrations before 0.28.0 were to be removed in 0.29 ([CHANGELOG](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md)).

## 3. How a Go service joins the tailnet (tsnet) and how the control plane reaches agents

**tsnet** is "a library that lets you embed Tailscale inside a Go program", using a userspace TCP/IP stack ([Tailscale tsnet KB](https://tailscale.com/kb/1244/tsnet)). Fields on `tsnet.Server`, from the doc comments in [tsnet.go (main branch)](https://raw.githubusercontent.com/tailscale/tailscale/main/tsnet/tsnet.go):

| Field | Behaviour |
|---|---|
| `ControlURL` | "optionally specifies the coordination server URL". If empty, falls back to env `TS_CONTROL_URL`, then the Tailscale default. This is the field that points tsnet at Headscale. |
| `AuthKey` | Auth key to create the node; preferred over env `TS_AUTHKEY` (then `TS_AUTH_KEY`). Not used if state already exists in the store. With existing state it is ignored unless `TSNET_FORCE_LOGIN=1`. |
| `Hostname` | Name presented to the control server; defaults to the binary name. |
| `Dir` / `Store` | State directory (default under `os.UserConfigDir`) and state store (default `Dir/tailscaled.state` file store). Must be unique per node when running several. |
| `Ephemeral` | Registers as an ephemeral node. |
| `AdvertiseTags` | Tags requested "for purposes of ACL enforcement"; the control server may refuse them. |
| `Port` | UDP port for WireGuard/peer traffic; zero means automatic. |
| `Tun` | Optional custom TUN device. Without it, netstack handles packets in-process, with no root or system daemon. |

Methods: `Up` (connect and wait until running), `Listen` (announces only on the tailnet), `Dial` (connects over the tailnet), `LocalClient`, `Loopback` (SOCKS5 proxy onto the tailnet). `Listen`, `Dial` and `LocalClient` start the server if needed.

- **Auth-key flow against Headscale:** set `ControlURL` to the Headscale `server_url`, set `AuthKey` to a pre-auth key (`hskey-auth-...`), set `Dir` to a persistent location. After first registration, state in `Dir` is reused and the key is ignored. The Tailscale KB page does not document `ControlURL`, `Dir` or `Ephemeral` (the doc comments in tsnet.go do). Whether tsnet against Headscale is a documented or tested combination: **unconfirmed**. headscale.net's client page and tools page make no statement about tsnet ([clients](https://headscale.net/stable/about/clients/), [tools](https://headscale.net/stable/ref/integration/tools/)).
- **Tagged keys and `AdvertiseTags`:** Headscale ignores client-requested tags on tagged pre-auth key registrations ("PreAuthKey tagged devices ignore `--advertise-tags` from clients", [CHANGELOG 0.28.0](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md); code: "Reject advertise-tags for PreAuthKey registrations early", [state.go](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/hscontrol/state/state.go)).
- **Control plane as a node:** a tsnet `Server` in the control plane would register like any node (tagged key, or a user-owned key). Whether it needs its own tag for ACL purposes is a policy choice, not a documented requirement.
- **Reaching agents:** the control plane dials an agent's tailnet IP (`100.64.0.0/10` default, `prefixes.v4`, plus ULA `fd7a:115c:a1e0::/48`, [config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)) or MagicDNS name with `Server.Dial`, and agents serve with `Server.Listen`. Direct paths use NAT traversal; otherwise traffic is relayed through DERP. The general DERP model (clients keep an outbound connection to a DERP server and are reachable through it) is Tailscale's design; I did not fetch a primary page that states outbound-only reachability in those words, so that framing is **unconfirmed**. Headscale's DERP page says the relay's "main purpose is to assist in node connectivity" ([DERP](https://headscale.net/stable/ref/derp/)).
- **Ephemeral nodes:** `node.ephemeral.inactivity_timeout` default 30m: "Time before an inactive ephemeral node is deleted" ([config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)).

## 4. Pre-auth keys, tags and policy via API/CLI/gRPC

### Pre-auth keys

- **REST** (base `/api/v1`, `Authorization: Bearer <API_KEY>`; schema served at `/swagger`; [API docs](https://headscale.net/stable/ref/api/)). The API docs page itself does not list preauthkey endpoints. They come from [headscale.proto](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/proto/headscale/v1/headscale.proto):

| RPC | Method and path |
|---|---|
| CreatePreAuthKey | `POST /api/v1/preauthkey` |
| ExpirePreAuthKey | `POST /api/v1/preauthkey/expire` |
| DeletePreAuthKey | `DELETE /api/v1/preauthkey` |
| ListPreAuthKeys | `GET /api/v1/preauthkey` |

- **`CreatePreAuthKeyRequest` fields** ([preauthkey.proto](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/proto/headscale/v1/preauthkey.proto)): `user` (uint64, 1), `reusable` (bool, 2), `ephemeral` (bool, 3), `expiration` (Timestamp, 4), `acl_tags` (repeated string, 5). `ExpirePreAuthKeyRequest` and `DeletePreAuthKeyRequest` take `id` (uint64). The `PreAuthKey` message carries `used`, `expiration`, `created_at`, `acl_tags`.
- **CLI** `headscale preauthkeys create` flags ([preauthkeys.go](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/cmd/headscale/cli/preauthkeys.go)): `--reusable` (default false), `--ephemeral` (default false), `--expiration/-e` (default `1h`), `--tags` (string slice), `--user/-u` (numeric ID). Docs example: `headscale preauthkeys create --tags tag:<TAG>`; docs say the default key "can be used once and is valid for one hour" ([Registration](https://headscale.net/stable/ref/registration/)).
- **Single-use:** `reusable=false` is the default. On use, the server runs an update guarded by `used = false` and returns "authkey already used" if no row changes, so concurrent registrations cannot both succeed ([preauth_keys.go](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/hscontrol/db/preauth_keys.go)). The code shown does not itself check `Reusable` before marking used; that check lives elsewhere (unconfirmed where).
- **Expiry:** set at creation via `expiration`; `ExpirePreAuthKey` sets expiration to now. If the gRPC request omits `expiration`, the server leaves the zero time (no expiry) rather than applying the CLI's 1h default; what the server then does with a zero time is **unconfirmed** ([grpcv1.go](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/hscontrol/grpcv1.go)). An API caller should send an explicit expiration.
- **Key format:** `hskey-auth-<12-char prefix>-<64-char secret>`; the secret is stored bcrypt-hashed and the full key is returned only once ([preauth_keys.go](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/hscontrol/db/preauth_keys.go)).
- **Owner/tags rule:** a key must have a user, tags, or both (`ErrPreAuthKeyNotTaggedOrOwned`). Each tag must begin with `tag:`, lowercase, no spaces (`validateTag` in grpcv1.go; `ErrPreAuthKeyACLTagInvalid` in the db layer). Tagged keys can be created with no user. Nodes registered with a tagged key are owned by the special user `tagged-devices` ([Registration](https://headscale.net/stable/ref/registration/)).
- **Tag ownership check on keys:** in the visible code, tags on a pre-auth key are not checked against `tagOwners`: the policy check `validateRequestTags` runs only for non-pre-auth-key registrations, and `CreatePreAuthKey` validates format only. Part of state.go (about 26k characters) and the db layer were not read, so treat this as **partly unconfirmed**. The registration docs also say they do not cover whether `tagOwners` applies to pre-auth keys. Practical reading: whoever holds Headscale API access can mint any `tag:` key, so Janus's control plane is the only enforcement point for `tag:acct-<id>` mapping.
- **Re-registration:** since 0.29.3, re-registering a tagged node with a different pre-auth key applies that key's tags (previously old tags were kept) ([release v0.29.3 via releases page](https://github.com/juanfont/headscale/releases)).
- **Node expiry:** tagged nodes are exempt from default expiry (0.29.0, "Tagged nodes (registered with tagged pre-auth keys) are exempt from default expiry"). New registrations with tagged keys have `Expiry = nil`. `node.expiry` config sets a default for nodes registered via auth key, `0` means never ([CHANGELOG](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md), [state.go](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/hscontrol/state/state.go), [config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)).

### Node tags

- Tags can be set on an existing node with `headscale nodes tag -i <ID> -t <TAG>` or API `POST /api/v1/node/{node_id}/tags` ([Tags](https://headscale.net/stable/ref/tags/), proto above). Applying a tag removes user ownership (0.28.0). Removing tags: re-authenticate with `--advertise-tags= --force-reauth` and register with `headscale auth register`.
- Tags can be `tagOwner` of other tags (0.28.0).
- Node API simplified in 0.28.0: `ForcedTags`, `InvalidTags`, `ValidTags` replaced by `Tags`; PreAuthKey CLI commands use IDs ([CHANGELOG](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md)).

### Policy format and runtime update

- Format: the same huJSON as Tailscale. Both `acls` and `grants` are supported (grants added in 0.29.0); the docs recommend grants because "ACLs are considered legacy". Omitting both means allow-all; `"grants": []` denies all ([Policy](https://headscale.net/stable/ref/policy/)).
- Supported autogroups: `internet`, `member`, `tagged`, `self`, `nonroot`, `danger-all`. Unsupported: device postures (`postures`, `srcPosture`), IP sets, and some autogroups (not listed). Since 0.29.0, `*` resolves to the CGNAT and ULA ranges, not all IPs ([Policy](https://headscale.net/stable/ref/policy/), [CHANGELOG](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md)).
- The Headscale policy page links to Tailscale docs for tags but does not describe `tagOwners` syntax itself. Syntax is therefore taken from Tailscale's format ([Tailscale ACL docs](https://tailscale.com/kb/1337/policy-syntax), not fetched in this pass: **unconfirmed** in detail).
- **Runtime update:**
  - `policy.mode: file` (default) with `policy.path`: changes need a reload via SIGHUP or `systemctl reload headscale` ([Policy](https://headscale.net/stable/ref/policy/)).
  - `policy.mode: database`: policy is stored in the DB. The API is `GET /api/v1/policy`, `PUT /api/v1/policy` (SetPolicy), `POST /api/v1/policy/check` (CheckPolicy). `SetPolicyRequest` carries a policy string; the response returns the policy and `updated_at` ([policy.proto](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/proto/headscale/v1/policy.proto), headscale.proto).
  - In `grpcv1.go`, `SetPolicy` returns `ErrPolicyUpdateIsDisabled` unless mode is `database`; it validates, saves, then calls `ReloadPolicy()` and pushes the resulting change set to connected nodes. When no nodes exist, the SSH-policy validation step is skipped.
  - The policy docs page does not document `policy.mode`, a `policy set/get` CLI or policy endpoints; these come from config-example.yaml and the code.
  - 0.29.5 note: "A rejected policy no longer leaves its packet filter active" ([releases](https://github.com/juanfont/headscale/releases)).
- **gRPC remote access:** default `grpc_listen_addr: 127.0.0.1:50443`; remote use needs TLS (or `grpc_allow_insecure`), an API key, and a matching-version `headscale` binary. gRPC through a reverse proxy is "not a supported feature". The REST gateway is served on the main HTTP listener under `/api/v1`. API keys: `headscale apikeys create` (default expiry 90 days, shown once), `apikeys expire --prefix`. Unix socket `/var/run/headscale/headscale.sock` gives local unauthenticated CLI access ([API docs](https://headscale.net/stable/ref/api/), [config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml)).
- A Go client library for the Headscale HTTP API exists but is third-party and "not maintained by the headscale authors" ([Tools](https://headscale.net/stable/ref/integration/tools/)). The proto files are in the repo for generating a client.

## 5. DERP relay requirements

**Headscale embedded DERP** ([DERP](https://headscale.net/stable/ref/derp/), [Requirements](https://headscale.net/stable/setup/requirements/), [config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/config-example.yaml), [derp_server.go](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/hscontrol/derp/server/derp_server.go)):
- Off by default; enable with `derp.server.enabled: true`. Default region id 999, code `headscale`.
- Ports: tcp/443 (shared with the control server, "Required if the embedded DERP server is enabled") and udp/3478 for STUN. `stun_listen_addr` "MUST be defined" when enabled. tcp/80 is needed only for Let's Encrypt HTTP-01.
- The DERP handler serves `/derp` and requires an `Upgrade` header of `websocket` or `derp`; behind a proxy WebSockets must pass through.
- `derp.server.ipv4` / `ipv6` are recommended for stability. `private_key_path` holds the DERP key. `automatically_add_embedded_derp_region: true` writes the region into the DERP map.
- `verify_clients: true` (default): only clients known to this Headscale may use the relay; failure does not fail open (code uses `SetVerifyClientURLFailOpen(false)`).
- TLS: the DERP docs page states no separate TLS requirement. DERP rides on the same HTTPS listener as the control server, so TLS is whatever terminates 443 (Headscale ACME/manual certs or a proxy). A public IP is required by the requirements page.
- Limitation: it "doesn't support the `/generate_204` endpoint via HTTP on port tcp/80", so captive-portal checks do not work against it.
- Throughput: "There are no speed or throughput optimisations, the main purpose is to assist in node connectivity."
- **Production suitability:** the docs make no explicit statement that it is or is not production-suitable. They warn that removing Tailscale's default DERP servers (`derp.urls: []`) leaves "just a single DERP server" and so a single point of failure. The code comments contain no production guidance. Anything stronger is **unconfirmed**.
- With a reverse proxy, STUN must still be reachable directly on udp/3478.

**Tailscale `derper`** ([custom DERP KB](https://tailscale.com/kb/1118/custom-derp-servers), [derper README](https://raw.githubusercontent.com/tailscale/tailscale/main/cmd/derper/README.md)):
- Open tcp 80, tcp 443, udp 3478 to the internet. Must not be behind NAT or a load balancer. Do not put it behind another HTTP proxy. Do not rate-limit UDP STUN or outbound TCP.
- Addresses in the DERP map must be publicly routable; static IPv4 and IPv6 are ideal and a DERP server should not share an IP with another.
- Install: `go install tailscale.com/cmd/derper@latest`; run e.g. `sudo derper --hostname=example.com`. Only Let's Encrypt certs rotate automatically; other cert changes need a restart.
- `--verify-clients` needs a `tailscaled` on the same host that is a member of the tailnet and can see every client in the ACL; `derper` and `tailscaled` should come from the same git revision. The Headscale docs describe pointing `-verify-client-url` at Headscale's `/verify` endpoint instead ([DERP](https://headscale.net/stable/ref/derp/)).
- Custom region ids 900 to 999 are reserved; one DERP server per region.
- Allow ICMP in and out. Monitor with `cmd/derpprobe`.
- DERP map delivery: Tailscale delivers it via the tailnet policy `derpMap` key; Headscale instead takes `derp.paths` (YAML file) or `derp.urls`, with `auto_update_enabled: true` and `update_frequency: 3h` defaults.
- Debug: `tailscale debug derp-map`, `tailscale debug derp headscale`.

## 6. Version compatibility and known limitations

- **Minimum Tailscale client** per Headscale line ([CHANGELOG](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md), [releases](https://github.com/juanfont/headscale/releases)): 0.27.x needs v1.64.0, 0.28.0 needs v1.74.0, 0.29.x needs v1.80.0. `/key` requests below the supported capability version are rejected as of 0.29.3.
- **General policy:** "We aim to support the last 10 releases of the Tailscale client on all provided operating systems and platforms." No known issues listed ([Clients](https://headscale.net/stable/about/clients/)).
- **tsnet version:** tsnet ships inside the `tailscale.com` Go module, so a tsnet-embedding agent takes the client version from the `tailscale.com` module version it pins. I did not find a primary page mapping tsnet module versions to Headscale versions. Whether tsnet at a given `tailscale.com` version meets the v1.80.0 minimum is therefore inferred, not stated: **unconfirmed**. The Go module version needs checking against `go.mod` at build time.
- **Grants:** supported from 0.29.0, including `app` (Taildrive, peer relay) and `via` ([CHANGELOG](https://raw.githubusercontent.com/juanfont/headscale/v0.29.5/CHANGELOG.md)). `via`-related fixes in 0.29.5 suggest the feature is still being stabilised. Postures and IP sets are unsupported ([Policy](https://headscale.net/stable/ref/policy/)).
- **Node expiry:** `node.expiry` default `0` (never) in config-example; tagged nodes exempt (0.29.0); `oidc.expiry` removed in favour of `node.expiry`. A 0.29.3 fix covers a tagged node stuck expired after `tailscale logout`.
- **Other limitations:** single tailnet; hostname collision suffixes became numeric in 0.29.0 so MagicDNS names can change on upgrade; `headscale nodes register` deprecated for `headscale auth register`; `auth/register` expects JSON; gRPC reflection removed from the remote server (0.29.0); deleting a node ends its long poll so the client must log in again (0.29.4).
- **Cloudflare Proxy/Tunnel in front of Headscale is unsupported** (no WebSocket POST support), relevant if the edge reverse proxy is Cloudflare ([Reverse proxy](https://headscale.net/stable/ref/integration/reverse-proxy/)).

## 7. Rough monthly cost (small VM, DERP traffic)

Prices retrieved 2026-10-19. Figures are list prices before VAT/tax unless stated. Bandwidth for DERP is outbound from the VM; inbound is free on all three providers named below that state it.

| Provider and plan | Compute | Included outbound | Overage | Notes and source |
|---|---|---|---|---|
| Hetzner Cloud CX23 (2 vCPU x86, 4 GB RAM, 40 GB NVMe) | EUR 5.49/month excl. VAT and IPv4, for new orders from 15 June 2026 | 20 TB listed on the plan page | Per-TB rate not on any fetched primary page; a third-party page says about $1/TB (**unconfirmed**) | [Hetzner price adjustment](https://docs.hetzner.com/general/infrastructure-and-availability/price-adjustment/), [cost-optimized plans](https://www.hetzner.com/cloud/cost-optimized). The IPv4 price is not stated on the fetched primary pages; third-party pages say EUR 0.50 (**unconfirmed**). The 20 TB applies to EU locations (NBG1, HEL1); the plan page showed "currently unavailable" for every plan at fetch time, and prices were blank there. US locations have far smaller allowances per a third-party source (**unconfirmed**). Billing: outgoing traffic only, billed in 100 MB blocks ([billing FAQ](https://docs.hetzner.com/cloud/billing/faq/)). |
| Hetzner CAX11 (2 vCPU Arm, 4 GB) | EUR 5.99/month excl. VAT and IPv4 | 20 TB | as above | same sources |
| DigitalOcean Basic Droplet | $4/month (1 vCPU, 512 MiB, 10 GiB) or $6/month (1 vCPU, 1 GiB, 25 GiB) or $12/month (1 vCPU, 2 GiB, 50 GiB) | 500 GiB, 1,000 GiB and 2,000 GiB respectively, pooled per team, no rollover | $0.01 per GiB | [Droplet pricing](https://www.digitalocean.com/pricing/droplets), [pricing docs](https://docs.digitalocean.com/products/droplets/details/pricing/) (last verified 25 Aug 2026 per the docs page). Inbound free. |
| Fly.io Machine, shared-cpu-1x | 256 MB $2.19/month, 512 MB $3.69/month, 1 GB $6.70/month (continuous, Ashburn region) | none stated (free inbound only) | $0.02/GB in North America and Europe, $0.04/GB in Asia Pacific, Oceania and South America, $0.12/GB in Africa and India | [Fly pricing](https://docs.fly.io/about/pricing). Dedicated IPv4 $2/month; shared IPv4 and IPv6 free; volumes $0.15/GB-month. UDP support (needed for STUN on udp/3478) is not stated on the pricing page: **unconfirmed**. Fly's suitability for running DERP/STUN was not checked. |
| Oracle Cloud Always Free | $0 | 10 TB outbound per month | not stated on the page | [Always Free resources](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm). Ampere A1: 2 OCPU and 12 GB total. Two AMD micro VMs, each 1/8 OCPU, 1 GB, up to 50 Mbps internet. 200 GB storage. Instances must be in the home region. Oracle "may reclaim idle" instances (95th-percentile CPU, network and memory, A1 only, below 20% over 7 days). Capacity errors ("out of host capacity") are documented. Reserved IP allowance not stated on the page. The oracle.com/cloud/free page returned HTTP 403. |

**Worked bandwidth examples** (arithmetic on the list prices above, not from the sources): relaying 1 TB of DERP traffic per month costs $0 on Hetzner EU CX23 (within 20 TB), about $0.24 on a DigitalOcean $6 Droplet (1,000 GiB included, 1,024 GiB sent, 24 GiB x $0.01), $20.48 on Fly (1,024 GB x $0.02 in North America or Europe) and $0 on Oracle Always Free (within 10 TB). A 50 Mbps cap on Oracle micro VMs bounds relay throughput to roughly 16 TB per month at 100% utilisation if the cap applies in both directions (own arithmetic).

**DERP traffic volume:** how much traffic Janus agents would send through DERP rather than directly depends on NAT traversal success and is not covered by any primary source: **unconfirmed**.

## Open items

1. Hetzner: overage rate per TB and IPv4 price on a primary page (the plan page and docs fetched had blank prices).
2. Whether `tagOwners` is enforced for tagged pre-auth keys (read `hscontrol/db/preauth_keys.go` callers and the rest of `state.go`), and exact `tagOwners` syntax in Headscale's policy (`hscontrol/policy`).
3. What the server does with a zero `expiration` on `CreatePreAuthKey`.
4. tsnet against Headscale: a live test with a pinned `tailscale.com` module version and the pinned Headscale release. Also read `/swagger` on the deployed version (as noted in `platform-fact-checks.md`).
5. Resource use (RAM per node, CPU under churn) and SQLite online backup: no primary figure found; measure.
6. Fly.io UDP/STUN support and whether it can host the embedded DERP.
7. Whether Headscale can run with a second instance or a read replica for HA: not documented.
