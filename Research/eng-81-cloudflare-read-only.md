# ENG-81: What Cloudflare can tell us about steven3zx.com through a read-only token

Research for the Origin Admin project (map ENG-80). Terms follow `portfolio/admin/GLOSSARY.md`: **origin host**, **inbound request**, **request trace**, **origin map**, **snapshot**, **history**.

Zone under study: `steven3zx.com`, DNS on Cloudflare, records proxied, plan believed to be Free. Researched 2026-10-19 from Cloudflare developer docs and API reference only. No API call was made: no token exists yet. Anything marked **verify live** should be confirmed with the token's first call.

## Answer in brief

- A single **zone-scoped API token restricted to `steven3zx.com`**, holding only `Read` permissions (list below), is enough for every Cloudflare card. No write or account-level permission is needed.
- **Settings:** each feature is read one setting at a time with `GET /zones/{zone_id}/settings/{setting_id}`. The "get all settings" call still exists but is marked deprecated. Rules (WAF, redirects, cache, transforms, DDoS overrides) are read per phase from the Rulesets API. Bot Fight Mode comes from `/bot_management`, and certificates from `/ssl/certificate_packs`.
- **Analytics:** since 2026-10-02, adaptive datasets (`httpRequestsAdaptiveGroups`, `firewallEventsAdaptive[Groups]`) keep **at least 31 days on every plan, including Free**, with up to 30 days per query. Aggregated datasets (`httpRequests1mGroups`/`1hGroups`/`1dGroups`) keep their older, shorter per-plan limits. The `settings` node of the GraphQL API gives the exact limits for this zone.
- **Security events:** these can be read **per event** on Free through GraphQL `firewallEventsAdaptive` (sampled), with fields such as action, source, client IP, ASN, country, path, query, user agent, Ray ID, and datetime. Retention is 31 days.
- **Per request:** **this has changed.** Since 2026-09-30, **Logpush is available on Free** with usage-based pricing (25 GB/month free to R2). Logpull stays Enterprise-only, and Instant Logs needs Business or higher. Logpush needs a *write* permission to set up, so a read-only token cannot use it. Without Logpush, the origin learns about the Cloudflare hop from request headers. `CF-Ray` gives a Ray ID with a 3-letter data-centre suffix. `CF-Connecting-IP` gives the visitor IP. `CF-IPCountry` (and optionally city, region, latitude/longitude, and timezone) gives location. `CF-Visitor` and `X-Forwarded-Proto` give the visitor's scheme.
- **Rate limits:** the REST API allows 1,200 requests per 5 minutes per user, shared by all of that user's tokens. GraphQL allows 300 queries per 5 minutes, and a query can cover up to 10 zones.

## 1. Token permission list

Create a **custom API token**. Under *Zone Resources*, set **Include → Specific zone → steven3zx.com**. Set every permission below to **Read**. Permission names come from the [API token permissions reference](https://developers.cloudflare.com/fundamentals/api/reference/permissions/).

| # | Permission (Zone scope) | Needed for |
|---|---|---|
| 1 | **Zone → Zone → Read** | `GET /zones/{zone_id}` for name, status, plan, and name servers. Also lets the token find the zone ID by name. |
| 2 | **Zone → Zone Settings → Read** | `GET /zones/{zone_id}/settings/{setting_id}` for SSL mode, Always Use HTTPS, HSTS, TLS, cache level, and so on ([API ref](https://developers.cloudflare.com/api/resources/zones/subresources/settings/methods/get/)). |
| 3 | **Zone → SSL and Certificates → Read** | `GET /zones/{zone_id}/ssl/certificate_packs` for edge certificates and their expiry ([API ref](https://developers.cloudflare.com/api/resources/ssl/subresources/certificate_packs/methods/list/)). |
| 4 | **Zone → Analytics → Read** | GraphQL Analytics API, zone-scoped queries ([auth docs](https://developers.cloudflare.com/analytics/graphql-api/getting-started/authentication/api-token-auth/)). |
| 5 | **Zone → Firewall Services → Read** | Firewall resources such as IP access rules. Security events themselves come through GraphQL under permission 4. |
| 6 | **Zone → Zone WAF → Read** | Rulesets in the WAF phases: custom rules, managed rules, rate limiting, and `ddos_l7` overrides. |
| 7 | **Zone → Bot Management → Read** | `GET /zones/{zone_id}/bot_management`, including Bot Fight Mode ([API ref](https://developers.cloudflare.com/api/resources/bot_management/methods/get/)). |
| 8 | **Zone → Page Rules → Read** | `GET /zones/{zone_id}/pagerules` for legacy Page Rules. |
| 9 | **Zone → Cache Settings → Read** (shown as "Cache Rules Read" in some places) | Cache Rules phase `http_request_cache_settings`. |
| 10 | **Zone → Single Redirect → Read** (also "Dynamic URL Redirects Read") | Single Redirects phase `http_request_dynamic_redirect`. |
| 11 | **Zone → Transform Rules → Read** | URL rewrite and header transform phases, plus Managed Transforms (which decide which `cf-*` headers reach the origin). |
| 12 | **Zone → Config Settings → Read** (also "Config Rules Read") | Configuration Rules phase `http_config_settings`. |
| 13 | **Zone → DNS → Read** | DNS records and their proxied status, to show which hostnames go through Cloudflare. |

Leave these out:

- **Logs Read / Logs Edit:** Logs Read means "read access to logs using Logpull" ([permissions](https://developers.cloudflare.com/fundamentals/api/reference/permissions/)), and Logpull is Enterprise-only ([Logpull](https://developers.cloudflare.com/logs/logpull/)). Creating a Logpush job is a write.
- **Account Analytics Read:** this is needed only for *account*-scoped GraphQL queries ([auth docs](https://developers.cloudflare.com/analytics/graphql-api/getting-started/authentication/api-token-auth/)). Every card here is zone-scoped.
- Anything ending in **Edit/Write**.

Caveat: the Rulesets API reference accepts "at least one" permission from a long list and does not document how each phase maps to a permission ([phases GET](https://developers.cloudflare.com/api/resources/rulesets/subresources/phases/methods/get/), [view rulesets](https://developers.cloudflare.com/ruleset-engine/rulesets-api/view/)). Permissions 6 and 9–12 are the per-product read permissions. **Verify live** that each phase's entry point returns 200 (or 404 when the phase has no ruleset) rather than 403.

You can check the token itself with `GET /user/tokens/verify`.

## 2. Readable zone features and settings

### How to tell which features are actually **on**

1. **Zone settings.** Read each relevant setting with `GET /zones/{zone_id}/settings/{setting_id}`. Each returns `{id, value, editable, modified_on}`. The call that lists every setting, `GET /zones/{zone_id}/settings`, still exists but is marked **deprecated**, so do not build on it ([settings resource](https://developers.cloudflare.com/api/resources/zones/subresources/settings/)). Fetch a fixed list of IDs every few minutes; the 1,200-request budget allows this easily.
2. **Rules.** `GET /zones/{zone_id}/rulesets` lists the rulesets in the zone's phases, without their rules. `GET /zones/{zone_id}/rulesets/phases/{phase}/entrypoint` returns the deployed rules for one phase. A **404 means nothing is deployed** in that phase ([view rulesets](https://developers.cloudflare.com/ruleset-engine/rulesets-api/view/)). For each rule, check `enabled` and `action`.
3. **Product endpoints.** Use `/bot_management` for bots, `/ssl/certificate_packs` for certificates, and `/pagerules` for Page Rules.

### Feature table

| Feature | Endpoint | Field / meaning of "on" | Free plan |
|---|---|---|---|
| SSL/TLS encryption mode | `GET /zones/{id}/settings/ssl` | `value` ∈ `off`, `flexible`, `full`, `strict` ([settings API](https://developers.cloudflare.com/api/resources/zones/subresources/settings/methods/get/)) | Yes |
| SSL/TLS Recommender | `/settings/ssl_recommender` | `value` on/off | Yes |
| Minimum TLS version | `/settings/min_tls_version` | `value` e.g. `1.2` | Yes |
| TLS 1.3 | `/settings/tls_1_3` | `value` | Yes |
| Always Use HTTPS | `/settings/always_use_https` | `value` = `on` | Yes |
| Automatic HTTPS Rewrites | `/settings/automatic_https_rewrites` | `value` | Yes |
| HSTS | `/settings/security_header` | `value.strict_transport_security.{enabled, max_age, include_subdomains, preload, nosniff}` | Yes |
| Opportunistic Encryption, 0-RTT, HTTP/2, HTTP/3 | `/settings/opportunistic_encryption`, `/0rtt`, `/http2`, `/http3` | `value` | Yes |
| Edge certificates | `GET /zones/{id}/ssl/certificate_packs` | `type` (`universal`/`advanced`/…), `status`, `hosts`, `certificate_authority`, `validity_days`, `certificates[].expires_on` ([API ref](https://developers.cloudflare.com/api/resources/ssl/subresources/certificate_packs/methods/list/)) | Universal certificate: yes |
| Cache level / Browser cache TTL / Development Mode / Always Online | `/settings/cache_level`, `/browser_cache_ttl`, `/development_mode`, `/always_online` | `value` (Development Mode also returns the time remaining) | Yes |
| Cache Rules | `/rulesets/phases/http_request_cache_settings/entrypoint` | rules with `enabled` | Yes (count limited by plan) |
| Security Level / Browser Integrity Check / Challenge TTL | `/settings/security_level`, `/browser_check`, `/challenge_ttl` | `value` | Yes |
| WAF custom rules | `/rulesets/phases/http_request_firewall_custom/entrypoint` | rules ([phases](https://developers.cloudflare.com/ruleset-engine/reference/phases-list/)) | Yes (a few rules) |
| WAF managed rules | `/rulesets/phases/http_request_firewall_managed/entrypoint` | an `execute` rule pointing at the **Cloudflare Free Managed Ruleset** | Free Managed Ruleset only. The Cloudflare Managed Ruleset, OWASP, and leaked-credentials rulesets are not on Free ([managed rules](https://developers.cloudflare.com/waf/managed-rules/)) |
| Rate limiting rules | `/rulesets/phases/http_ratelimit/entrypoint` | rules | Limited on Free |
| Legacy WAF toggle | `/settings/waf` | refers to the **deprecated** older managed rules, so ignore it | — |
| Bot Fight Mode | `GET /zones/{id}/bot_management` | `fight_mode` (bool), `enable_js`, `ai_bots_protection`, `crawler_protection`, … The Free-zone response shape is the Bot Fight Mode variant; **verify live** ([API ref](https://developers.cloudflare.com/api/resources/bot_management/methods/get/)) | Yes |
| HTTP DDoS protection | Always on. Overrides live in `/rulesets/phases/ddos_l7/entrypoint` | Always enabled; non-Enterprise zones can have one override and cannot change its expression ([HTTP DDoS](https://developers.cloudflare.com/ddos-protection/managed-rulesets/http/)) | Yes, always on |
| Page Rules (legacy) | `GET /zones/{id}/pagerules` | `status` = `active` | Yes (3) |
| Single Redirects | `/rulesets/phases/http_request_dynamic_redirect/entrypoint` | rules | Yes |
| Transform rules / Managed Transforms | `/rulesets/phases/http_request_late_transform/…`, `http_response_headers_transform`, managed-headers endpoint | which request headers get added | Yes |
| IP Geolocation (`CF-IPCountry`) | `/settings/ip_geolocation` | `value` = `on` ([IP geolocation](https://developers.cloudflare.com/network/ip-geolocation/)) | Yes |
| Pseudo IPv4, True-Client-IP | `/settings/pseudo_ipv4`, `/true_client_ip_header` | `value` | True-Client-IP is Enterprise-only |
| Proxied hostnames | `GET /zones/{id}/dns_records` | `proxied: true` | Yes |

## 3. Analytics through the GraphQL Analytics API

Endpoint: `POST https://api.cloudflare.com/client/v4/graphql`. Queries are zone-scoped via `viewer { zones(filter: {zoneTag: $zoneTag}) { … } }`.

| Dataset | Kind | Granularity | What it gives | Retention on Free |
|---|---|---|---|---|
| `httpRequestsAdaptiveGroups` | Adaptive (sampled), aggregated | Any dimension down to `datetimeMinute`; `count`, `sum{edgeResponseBytes, visits}`, `avg{sampleInterval}`; dimensions such as `coloCode` ([migration guide](https://developers.cloudflare.com/analytics/graphql-api/migration-guides/graphql-api-analytics/)), plus country, status, cache status, host, path (**verify live**) | Requests, bandwidth, cache status, status codes, countries, data centres | **≥ 31 days, up to 30 days per query, all plans** since 2026-10-02 ([changelog](https://developers.cloudflare.com/changelog/post/2026-10-02-30-days-analytics-on-every-plan/), [limits](https://developers.cloudflare.com/analytics/graphql-api/limits/)) |
| `httpRequests1mGroups` / `1hGroups` / `1dGroups` | Aggregated rollups (not sampled) | Fixed buckets: minute or five minutes (`datetimeFiveMinutes`), hour, day | `sum{requests, bytes, cachedRequests, cachedBytes, encryptedRequests, threats, pageViews, countryMap, responseStatusMap, browserMap, contentTypeMap, clientSSLMap, ipClassMap, threatPathingMap}`, `uniq{uniques}` ([zone-analytics migration](https://developers.cloudflare.com/analytics/graphql-api/migration-guides/zone-analytics/)) | "Keep their existing per-plan limits." Before the change, Free history ranged from **24 hours to 8 days** depending on dataset ([changelog](https://developers.cloudflare.com/changelog/post/2026-10-02-30-days-analytics-on-every-plan/)). The exact numbers for this zone come from the `settings` node. |
| `firewallEventsAdaptiveGroups` | Adaptive, aggregated | to `datetimeMinute` / `datetimeHour` | Counts of threats and mitigations by action, source, country, and path | ≥ 31 days |
| `firewallEventsAdaptive` | Adaptive, **per event** | each event | see §4 | 31 days; example limits are `maxDuration` 3 days, `maxPageSize` 10,000 ([settings node](https://developers.cloudflare.com/analytics/graphql-api/features/discovery/settings/)) |
| `httpRequestsAdaptive` (raw, non-Groups) | Adaptive, per request | per request | — | Not documented as available on Free. **Verify via `settings`**; assume not available. |

Mapping the card metrics:

- **Requests:** `count` (adaptive) or `sum.requests` (rollup).
- **Bandwidth:** `sum.edgeResponseBytes` or `sum.bytes`.
- **Cache ratio:** `sum.cachedRequests / sum.requests` from rollups, or group adaptive results by cache status.
- **Status codes:** `responseStatusMap` or an edge response status dimension.
- **Countries:** `countryMap` or a country dimension.
- **Threats:** `sum.threats` plus the firewall datasets.

Sampling: datasets named "Adaptive" are sampled more heavily as volume rises. Counts are scaled back up, and small volumes are not sampled ([sampling](https://developers.cloudflare.com/analytics/graphql-api/sampling/)). A personal site will mostly see unsampled data.

**Exact limits for this zone.** Ask the `settings` node once at startup and size every query to what it returns:

```graphql
query ($zoneTag: String) {
  viewer { zones(filter: {zoneTag: $zoneTag}) { settings {
    httpRequestsAdaptiveGroups { enabled maxDuration notOlderThan maxPageSize availableFields }
    httpRequests1mGroups       { enabled maxDuration notOlderThan }
    httpRequests1hGroups       { enabled maxDuration notOlderThan }
    httpRequests1dGroups       { enabled maxDuration notOlderThan }
    firewallEventsAdaptive     { enabled maxDuration notOlderThan maxPageSize availableFields }
    httpRequestsAdaptive       { enabled }
  } } }
}
```

`notOlderThan` and `maxDuration` are in seconds ([settings node](https://developers.cloudflare.com/analytics/graphql-api/features/discovery/settings/)).

## 4. Security events per event

Yes, on Free. Security Events works on Free, but the dashboard shows **sampled logs only**, with no export. Retention is 31 days and the maximum query window is 30 days. The underlying dataset is **`firewallEventsAdaptive`** ([Security Events](https://developers.cloudflare.com/waf/analytics/security-events/)).

Fields used in Cloudflare's own example query: `action`, `clientAsn`, `clientCountryName`, `clientIP`, `clientRequestPath`, `clientRequestQuery`, `datetime`, `source` (the product that acted: WAF, Bot Fight Mode, rate limiting, and so on), and `userAgent` ([querying firewall events](https://developers.cloudflare.com/analytics/graphql-api/tutorials/querying-firewall-events/)). The dataset also carries the Ray ID (`rayName`) and rule identifiers. Cloudflare says all customers can filter Security Events by Ray ID ([Ray ID](https://developers.cloudflare.com/fundamentals/reference/cloudflare-ray-id/)). Use the `settings` node's `availableFields` to get the exact field list on Free.

Because events carry the Ray ID, Origin Admin can join a blocked or challenged request to its event. Requests Cloudflare blocks never reach the origin host, so these events are the **only** record of them.

## 5. Per-request data and the Cloudflare hop

### Per-request products, by plan

| Product | Free | Notes |
|---|---|---|
| **Logpush** | **Yes, since 2026-09-30** | Self-service on Free, Pro, and Business with usage-based pricing: 25 GB/month free to internal destinations (R2, Pipelines), then $0.03/GB; 25 GB/month free to external destinations, then $0.10/GB ([changelog](https://developers.cloudflare.com/changelog/post/2026-09-30-logpush-usage-based-pricing/), [pricing](https://developers.cloudflare.com/logs/logpush/pricing/), [Logpush](https://developers.cloudflare.com/logs/logpush/)). Which fields are available "depends on your subscription plan" ([datasets](https://developers.cloudflare.com/logs/logpush/logpush-job/datasets/)); list them with `GET /zones/{id}/logpush/datasets/http_requests/fields`. Creating a job needs a **Logs write** permission, so it is outside the read-only token. |
| Instant Logs | No (Business and Enterprise) | [Instant Logs](https://developers.cloudflare.com/logs/instant-logs/) |
| Logpull | No (Enterprise only) | [Logpull](https://developers.cloudflare.com/logs/logpull/) |
| GraphQL raw `httpRequestsAdaptive` | Not documented for Free | Check with the `settings` node |

**This changes the ticket's assumption.** Per-request edge logs are now possible on Free, by pushing the `http_requests` dataset to R2 or to an HTTP endpoint on the origin host. Setting that up is a one-time configuration step with a write permission, done by hand or with a separate token. It is not something the read-only Origin Admin token can do. Reading the pushed logs afterwards happens in R2 or on the origin host, not through the Cloudflare API.

### What the origin learns from headers (no extra product)

For every inbound request that Cloudflare proxies, the origin host (Caddy) receives these headers ([HTTP headers reference](https://developers.cloudflare.com/fundamentals/reference/http-headers/), [Managed Transforms](https://developers.cloudflare.com/rules/transform/managed-transforms/reference/)):

| Header | What it tells the request trace | Free |
|---|---|---|
| `CF-Ray` | Ray ID, like `<hex>-<IATA>`. The **three-letter suffix is the Cloudflare data centre** that handled the request. With Argo or Tiered Cache it may be the data centre that connected to the origin rather than the ingress one. Ray IDs are "not guaranteed to be unique" ([Ray ID](https://developers.cloudflare.com/fundamentals/reference/cloudflare-ray-id/)). | Yes |
| `CF-Connecting-IP` | The real visitor IP. Sent only on Cloudflare-to-origin traffic. | Yes |
| `X-Forwarded-For` | The visitor IP, with any earlier proxies before it. Cloudflare recommends `CF-Connecting-IP` instead. | Yes |
| `CF-Connecting-IPv6` | The real IPv6 address when Pseudo IPv4 overwrites headers | Yes |
| `CF-IPCountry` | ISO country; `XX` means unknown and `T1` means Tor. Sent when IP Geolocation is on or the location Managed Transform is on. | Yes |
| `cf-ipcity`, `cf-ipcontinent`, `cf-iplatitude`, `cf-iplongitude`, `cf-region`, `cf-region-code`, `cf-metro-code`, `cf-postal-code`, `cf-timezone` | Visitor location, when the **"Add visitor location headers"** Managed Transform is on. No plan restriction is stated. | Likely; docs state no plan requirement (**verify live**) |
| `CF-Visitor` | `{"scheme":"https"}`: the scheme the visitor used to reach Cloudflare | Yes |
| `X-Forwarded-Proto` | Same information, as `http` or `https` | Yes |
| `CDN-Loop` | Contains `cloudflare`; loop detection | Yes |
| `CF-Worker` | Present if a Worker made the subrequest | Yes |
| `True-Client-IP` | Same as `CF-Connecting-IP` | Enterprise only |
| `cf-bot-score`, `cf-verified-bot`, `cf-ja3-hash`, `cf-ja4` | Bot signals | Enterprise Bot Management only |

What a request trace **can** say about the Cloudflare hop:

- That the request came through Cloudflare. Also check the TCP peer address against Cloudflare's published IP ranges (`GET /ips`, no auth needed).
- Which data centre handled it, from the `CF-Ray` suffix.
- The real client IP, country, and optionally city, region, and timezone.
- Whether the visitor used HTTP or HTTPS.
- The Ray ID, as a key for joining to Security Events.

What it **cannot** say from headers alone:

- Time spent at the edge, or the visitor-to-edge RTT.
- Edge cache status. `CF-Cache-Status` is a *response* header added on the way back to the visitor, so the origin never sees a cache HIT at all.
- Which WAF or bot rules were evaluated.
- Edge TLS version or cipher.
- Edge response status or bytes after Cloudflare changed the response.

These need Logpush `http_requests` records, joined by Ray ID, or the aggregated GraphQL analytics.

## 6. Rate limits

| API | Limit | Source |
|---|---|---|
| Cloudflare REST API (all endpoints) | **1,200 requests per 5 minutes per user**, shared by the dashboard, API keys, and API tokens. Going over it returns HTTP 429 and blocks calls for 5 minutes. Responses carry `Ratelimit` and `Ratelimit-Policy` headers, and `retry-after` once you are over. | [API limits](https://developers.cloudflare.com/fundamentals/api/reference/limits/) |
| GraphQL Analytics API | **300 queries per 5 minutes** per user by default (the general limits page says "max 320/5 min, varies by query cost"). A zone-scoped query can cover up to 10 zones. Account-based rate limiting is opt-in. The REST limit applies on top. | [GraphQL limits](https://developers.cloudflare.com/analytics/graphql-api/limits/), [API limits](https://developers.cloudflare.com/fundamentals/api/reference/limits/) |
| Per-query node limits | `maxDuration`, `notOlderThan`, `maxNumberOfFields`, and `maxPageSize` for each dataset, read from the `settings` node | [settings node](https://developers.cloudflare.com/analytics/graphql-api/features/discovery/settings/) |

Budget sketch: polling about 20 settings and rulesets every 5 minutes, plus one GraphQL query a minute, uses about 25 REST calls and 5 GraphQL queries per window. That is far below both limits. Even a 1-minute settings refresh stays under 10% of the REST limit.

## Open items to verify with the first live call

1. Confirm the zone's plan: `GET /zones/{id}` → `plan.legacy_id` / `plan.name`.
2. Call the `settings` node for exact Free retention of the `httpRequests1m/1h/1dGroups` datasets, and to see whether `httpRequestsAdaptive` (raw) is enabled.
3. Confirm each `rulesets/phases/{phase}/entrypoint` returns 200 or 404 with this token, not 403.
4. Check the `availableFields` of `firewallEventsAdaptive` on Free.
5. Decide whether to enable Logpush (`http_requests` → R2, or → an HTTPS endpoint on the origin host) as a separate one-off step with a write-capable token.
