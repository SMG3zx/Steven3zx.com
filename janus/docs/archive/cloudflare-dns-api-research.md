# Cloudflare DNS API Research Report

## For Janus Platform Domain Management

---

## 1. Cloudflare API v4 DNS Endpoints and Patterns

### Base URL
```
https://api.cloudflare.com/client/v4/
```

### DNS Record CRUD Endpoints

| Operation | Method | Endpoint |
|-----------|--------|----------|
| **Create** | `POST` | `/zones/{zone_id}/dns_records` |
| **List** | `GET` | `/zones/{zone_id}/dns_records` |
| **Get** | `GET` | `/zones/{zone_id}/dns_records/{dns_record_id}` |
| **Update (partial)** | `PATCH` | `/zones/{zone_id}/dns_records/{dns_record_id}` |
| **Overwrite (full)** | `PUT` | `/zones/{zone_id}/dns_records/{dns_record_id}` |
| **Delete** | `DELETE` | `/zones/{zone_id}/dns_records/{dns_record_id}` |
| **Batch** | `POST` | `/zones/{zone_id}/dns_records/batch` |
| **Export** | `GET` | `/zones/{zone_id}/dns_records/export` |
| **Import** | `POST` | `/zones/{zone_id}/dns_records/import` |

### Authentication
```
Authorization: Bearer <CLOUDFLARE_API_TOKEN>
```

### Supported Record Types for Janus
- **A** — IPv4 address records (for pointing to server IPs)
- **AAAA** — IPv6 address records
- **CNAME** — Canonical name aliases (for custom domains pointing to platform)
- **TXT** — Text records (for domain verification)

### Create DNS Record Example (cURL)
```bash
curl https://api.cloudflare.com/client/v4/zones/$ZONE_ID/dns_records \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
    -d '{
          "name": "myapp.apps.janus.example.com",
          "ttl": 1,
          "type": "A",
          "content": "198.51.100.4",
          "proxied": true,
          "comment": "Janus deployment: project-abc"
        }'
```

### Batch Operations
The batch endpoint (`POST /zones/{zone_id}/dns_records/batch`) executes multiple operations atomically in this order: Deletes → Patches → Puts → Posts. This is ideal for bulk deployment operations.

### Key Constraints
- A/AAAA records cannot exist on the same name as CNAME records
- NS records cannot exist on the same name as any other record type
- Domain names are always in Punycode
- TTL: 1 = "automatic"; otherwise 60-86400 seconds (30 min for Enterprise)

---

## 2. cloudflare-go Library Usage and Patterns

### Library Details
- **Package**: `github.com/cloudflare/cloudflare-go/v3` (latest: v6.8.0, Feb 2026)
- **License**: Apache-2.0
- **Stars**: 1.9k | **Used by**: 7k+ projects
- **Auto-generated** from Cloudflare OpenAPI spec via Stainless

### Client Initialization
```go
import (
    "context"
    "github.com/cloudflare/cloudflare-go/v3"
    "github.com/cloudflare/cloudflare-go/v3/dns"
    "github.com/cloudflare/cloudflare-go/v3/option"
    "github.com/cloudflare/cloudflare-go/v3/custom_hostnames"
)

client := cloudflare.NewClient(
    option.WithAPIToken("your-api-token"),
)
```

### Create DNS Record (Go)
```go
recordResponse, err := client.DNS.Records.New(context.TODO(), dns.RecordNewParams{
    ZoneID: cloudflare.F("zone-id-here"),
    Body: dns.ARecordParam{
        Name:    cloudflare.F("myapp.apps.janus.example.com"),
        Content: cloudflare.F("198.51.100.4"),
        TTL:     cloudflare.F(dns.TTL1), // automatic
        Type:    cloudflare.F(dns.ARecordTypeA),
        Proxied: cloudflare.F(true),
        Comment: cloudflare.F("Janus deployment record"),
    },
})
```

### Create CNAME Record (Go)
```go
recordResponse, err := client.DNS.Records.New(context.TODO(), dns.RecordNewParams{
    ZoneID: cloudflare.F("zone-id-here"),
    Body: dns.CNAMERecordParam{
        Name:    cloudflare.F("myapp.apps.janus.example.com"),
        Content: cloudflare.F("loadbalancer.janus.internal"),
        TTL:     cloudflare.F(dns.TTL1),
        Type:    cloudflare.F(dns.CNAMERecordTypeCNAME),
        Proxied: cloudflare.F(true),
    },
})
```

### List DNS Records
```go
records, err := client.DNS.Records.List(context.TODO(), dns.RecordListParams{
    ZoneID: cloudflare.F("zone-id-here"),
    // Optionally filter by name, type, etc.
})
```

### Update DNS Record
```go
recordResponse, err := client.DNS.Records.Edit(context.TODO(), "dns-record-id", dns.RecordEditParams{
    ZoneID: cloudflare.F("zone-id-here"),
    Body: dns.ARecordParam{
        Content: cloudflare.F("198.51.100.5"), // new IP
    },
})
```

### Delete DNS Record
```go
_, err := client.DNS.Records.Delete(context.TODO(), "dns-record-id", dns.RecordDeleteParams{
    ZoneID: cloudflare.F("zone-id-here"),
})
```

### Batch Operations
```go
batchResponse, err := client.DNS.Records.Batch(context.TODO(), dns.RecordBatchParams{
    ZoneID: cloudflare.F("zone-id-here"),
    // Posts, Puts, Patches, Deletes arrays
})
```

### Built-in Features
- **Auto-retry** with backoff on rate limits (respects `Ratelimit` headers)
- **Pagination** support (V4PagePaginationArray)
- **Configurable retries**: `option.WithMaxRetries(5)`
- **Custom HTTP client**: `option.WithHTTPClient(httpClient)`
- **Middleware support**: `option.WithMiddleware(loggerMiddleware)`
- **Raw response access**: `option.WithResponseInto(&response)`

---

## 3. Platform Subdomain Strategy

### Option A: Wildcard DNS Record (Recommended for Platform Subdomains)

Set up a single wildcard DNS record:
```
*.apps.janus.example.com → CNAME → loadbalancer.janus.example.com (Proxied)
```

**Pros:**
- Zero DNS propagation delay for new deployments
- No per-deployment API calls needed for DNS
- Unlimited subdomains with single record
- Routing handled at application layer (reverse proxy / ingress)

**Cons:**
- All subdomains resolve, even non-existent ones (must handle at app layer)
- Wildcard SSL requires specific handling (Cloudflare auto-handles with proxy)

**How it works:**
1. Create wildcard CNAME: `*.apps.janus.example.com → lb.janus.example.com` (proxied)
2. On deployment, assign a subdomain like `{project-slug}.apps.janus.example.com`
3. Reverse proxy / ingress controller routes based on Host header
4. No Cloudflare API call needed per deployment

### Option B: Per-Deployment DNS Records

Create individual A/CNAME records per deployment.

**Pros:**
- Explicit control over which subdomains exist
- Can point different deployments to different origins

**Cons:**
- API call per deployment (creates/deletes)
- DNS propagation delay (typically seconds with Cloudflare proxy, but up to TTL)
- Must manage record lifecycle (create on deploy, delete on teardown)

### Recommendation for Janus
**Use wildcard DNS (Option A)** for platform subdomains. This is the pattern used by Vercel, Netlify, and other PaaS platforms. Routing is handled at the reverse proxy / ingress layer, not DNS.

---

## 4. Custom Domain Verification Flow Design

### Industry Standard Flow (Vercel/Netlify Pattern)

The standard SaaS custom domain flow has these steps:

#### Step 1: User Initiates Domain Binding
User provides their custom domain (e.g., `app.customer.com`) in the Janus UI.

#### Step 2: Janus Generates Verification Token
Janus generates a unique verification token and instructs the user:
```
Add a CNAME record:
  app.customer.com → proxy-target.janus.example.com

AND/OR add a TXT record for verification:
  _janus-verification.app.customer.com → janus-verify=abc123def456
```

#### Step 3: User Configures DNS
User adds records at their DNS provider. Two approaches:

**Approach A — CNAME-based (simpler, recommended):**
```
app.customer.com CNAME proxy-target.janus.example.com
```
The CNAME itself proves ownership. Janus checks if the domain resolves to the expected target.

**Approach B — TXT-based verification (more explicit):**
```
_janus-verification.app.customer.com TXT "janus-verify=<token>"
```
Janus performs DNS lookup for the TXT record. After verification, user sets CNAME.

#### Step 4: Janus Verifies Ownership
Janus periodically checks (with backoff):
1. DNS lookup for CNAME or TXT record
2. Verify value matches expected token/target
3. Mark domain as "verified" in database

#### Step 5: SSL Certificate Provisioning
Once verified, provision SSL (see Section 5 below).

#### Step 6: Traffic Routing
Configure reverse proxy to route `app.customer.com` traffic to the correct deployment.

### Verification States
```
pending → dns_configured → verified → active → (deactivated)
```

### Backoff Schedule for Polling
- First check: immediate
- 0-5 min: every 30 seconds
- 5-30 min: every 2 minutes
- 30 min-24h: every 15 minutes
- After 24h: every hour (up to 7 days, then expire)

---

## 5. Cloudflare for SaaS (SSL for SaaS) Setup

### What It Is
Cloudflare for SaaS (formerly "SSL for SaaS") is the **recommended approach** for handling custom domains on a SaaS platform. It provides:
- Automatic SSL certificate provisioning for customer domains
- Traffic routing through Cloudflare's network
- DDoS protection, WAF, and CDN for all custom hostnames
- No need to manage certificates yourself

### How It Works

#### Initial Setup (One-time)

1. **Add zone to Cloudflare** (e.g., `janus.example.com`)
2. **Enable Cloudflare for SaaS** on the zone
3. **Create a fallback origin** — where custom hostname traffic routes:
   ```
   proxy-fallback.janus.example.com → A → <your-server-ip> (Proxied)
   ```
4. **Create a CNAME target** (optional, recommended):
   ```
   *.customers.janus.example.com → CNAME → proxy-fallback.janus.example.com (Proxied)
   ```

#### Per-Customer Setup

1. **Create Custom Hostname** via API:
   ```go
   hostname, err := client.CustomHostnames.New(context.TODO(), custom_hostnames.CustomHostnameNewParams{
       ZoneID:   cloudflare.F("zone-id"),
       Hostname: cloudflare.F("app.customer.com"),
       SSL: cloudflare.F(custom_hostnames.CustomHostnameNewParamsSSL{
           Method: cloudflare.F(custom_hostnames.CustomHostnameNewParamsSSLMethodHTTP),
           Type:   cloudflare.F(custom_hostnames.CustomHostnameNewParamsSSLTypeDV),
       }),
   })
   ```

2. **Customer creates CNAME** pointing to your CNAME target:
   ```
   app.customer.com CNAME customers.janus.example.com
   ```

3. **Certificate validation** happens automatically (HTTP method) or via TXT record
4. **Hostname validation** — Cloudflare verifies the CNAME points correctly
5. **SSL issued automatically** — Cloudflare provisions a DV certificate

### Certificate Validation Methods

| Method | Customer Effort | Downtime |
|--------|----------------|----------|
| **HTTP (automatic)** | Just set CNAME | Brief (~minutes) |
| **HTTP (manual)** | Place token on origin | None |
| **TXT** | Add TXT record | None |
| **Delegated DCV** | One-time CNAME | None (auto-renews) |

### Pre-validation (TXT-based)
For zero-downtime onboarding:
```json
{
  "ownership_verification": {
    "type": "txt",
    "name": "_cf-custom-hostname.app.customer.com",
    "value": "unique-verification-token"
  }
}
```

### Pricing (Cloudflare for SaaS)

| Plan | Included Hostnames | Max Hostnames | Per Additional |
|------|-------------------|---------------|----------------|
| Free | 100 | 50,000 | $0.10/month |
| Pro | 100 | 50,000 | $0.10/month |
| Business | 100 | 50,000 | $0.10/month |
| Enterprise | Custom | Unlimited | Custom |

### Key Limitations
- Wildcard custom hostnames: Enterprise only
- Custom certificates: Enterprise only
- Selectable CA: Enterprise only
- mTLS support: Enterprise only
- Customer domains already on Cloudflare have some feature restrictions

---

## 6. Required API Token Permissions

### For DNS Record Management Only

**Zone-level permissions needed:**
| Permission | Access | Purpose |
|-----------|--------|---------|
| **DNS Write** | Zone | Create, update, delete DNS records |
| **DNS Read** | Zone | List and read DNS records |

### For Cloudflare for SaaS (Custom Hostnames)

**Zone-level permissions needed:**
| Permission | Access | Purpose |
|-----------|--------|---------|
| **SSL and Certificates Write** | Zone | Manage custom hostnames and SSL |
| **SSL and Certificates Read** | Zone | Read custom hostname status |
| **DNS Write** | Zone | Manage fallback origin and CNAME target |
| **DNS Read** | Zone | Read DNS records |

### For Full Janus Domain Management

**Recommended token configuration:**
```
Zone Permissions:
  - DNS: Edit (read + write)
  - SSL and Certificates: Edit (read + write)
  - Zone: Read (to list zones)

Zone Resources:
  - Include: Specific zone(s) for Janus platform domain
```

### Token Security Best Practices
- Create a dedicated API token (not global API key)
- Scope to specific zone(s) only
- Use IP filtering if API calls come from fixed IPs
- Rotate tokens periodically
- Store in secrets management (not env vars)

---

## 7. API Rate Limits

| Limit Type | Value |
|------------|-------|
| **Client API per user/token** | 1,200 requests / 5 minutes |
| **Client API per IP** | 200 requests / second |
| **GraphQL** | Varies by query cost, max 320 / 5 min |
| **User API token quota** | 50 tokens |
| **Account API token quota** | 500 tokens |

### Rate Limit Headers
```
Ratelimit: "default";r=50;t=30
Ratelimit-Policy: "burst";q=100;w=60
retry-after: <seconds>  (only when rate limited)
```

### Implications for Janus
- At 1,200/5min (~4/sec), Janus can handle significant deployment volume
- Batch endpoint reduces API calls (multiple records in single request)
- Wildcard DNS strategy eliminates per-deployment DNS calls
- cloudflare-go auto-handles rate limit backoff
- Enterprise customers can request higher limits

---

## 8. Recommended Architecture for Janus Domain Management

### Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                    Janus Platform                         │
│                                                           │
│  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐ │
│  │  Domain       │   │  DNS Manager │   │  SSL Manager │ │
│  │  Service      │──→│  (CF API)    │   │  (CF4SaaS)   │ │
│  │  (Go)         │   │              │   │              │ │
│  └──────┬───────┘   └──────────────┘   └──────────────┘ │
│         │                                                 │
│  ┌──────▼───────────────────────────────────────────────┐ │
│  │            Reverse Proxy / Ingress                    │ │
│  │    Routes by Host header to correct deployment        │ │
│  └───────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────┐
│                    Cloudflare                             │
│                                                           │
│  Zone: janus.example.com                                  │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ DNS Records:                                         │ │
│  │  *.apps.janus.example.com  CNAME  lb.janus.ex...    │ │
│  │  lb.janus.example.com     A       <LB_IP>           │ │
│  │  *.customers.janus.ex...  CNAME  proxy-fallback..   │ │
│  │  proxy-fallback.janus...  A       <ORIGIN_IP>       │ │
│  └─────────────────────────────────────────────────────┘ │
│  ┌─────────────────────────────────────────────────────┐ │
│  │ Custom Hostnames (CF for SaaS):                      │ │
│  │  app.customer1.com → proxy-fallback.janus.ex...     │ │
│  │  dashboard.customer2.io → proxy-fallback.janus...   │ │
│  └─────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

### Two Domain Strategies in One System

#### 1. Platform Subdomains (Automatic, via wildcard DNS)
- Pattern: `{slug}.apps.janus.example.com`
- Implementation: Single wildcard CNAME record, routing at ingress layer
- No API calls needed per deployment
- Instant availability

#### 2. Custom Domains (User-provided, via Cloudflare for SaaS)
- Pattern: User's own domain (e.g., `app.customer.com`)
- Implementation: Cloudflare for SaaS Custom Hostnames API
- Automated SSL provisioning
- Verification flow with TXT or HTTP-based ownership proof

### Go Service Design

```go
// domain_service.go - Core domain management service

type DomainService struct {
    cfClient    *cloudflare.Client
    zoneID      string
    db          *sql.DB
}

// For platform subdomains - just database + ingress config, no Cloudflare API needed
func (s *DomainService) AssignPlatformSubdomain(ctx context.Context, projectSlug string) (string, error) {
    subdomain := fmt.Sprintf("%s.apps.janus.example.com", projectSlug)
    // Save to DB, configure ingress routing
    // No Cloudflare API call needed (wildcard handles it)
    return subdomain, nil
}

// For custom domains - uses Cloudflare for SaaS
func (s *DomainService) AddCustomDomain(ctx context.Context, hostname string) (*DomainBinding, error) {
    // 1. Create custom hostname in Cloudflare
    ch, err := s.cfClient.CustomHostnames.New(ctx, custom_hostnames.CustomHostnameNewParams{
        ZoneID:   cloudflare.F(s.zoneID),
        Hostname: cloudflare.F(hostname),
        SSL: cloudflare.F(custom_hostnames.CustomHostnameNewParamsSSL{
            Method: cloudflare.F(custom_hostnames.CustomHostnameNewParamsSSLMethodHTTP),
            Type:   cloudflare.F(custom_hostnames.CustomHostnameNewParamsSSLTypeDV),
        }),
    })
    if err != nil {
        return nil, fmt.Errorf("creating custom hostname: %w", err)
    }
    
    // 2. Extract verification info
    // 3. Save to DB with "pending" status
    // 4. Return instructions for customer
    return &DomainBinding{
        Hostname:     hostname,
        Status:       "pending",
        CNAMETarget:  "customers.janus.example.com",
        Verification: ch.OwnershipVerification,
    }, nil
}

// Verification poller
func (s *DomainService) CheckDomainVerification(ctx context.Context, hostnameID string) error {
    ch, err := s.cfClient.CustomHostnames.Get(ctx, hostnameID, custom_hostnames.CustomHostnameGetParams{
        ZoneID: cloudflare.F(s.zoneID),
    })
    if err != nil {
        return err
    }
    // Check ch.Status - update DB accordingly
    // Status: "pending" | "active" | "moved" | "deleted" | etc.
    return nil
}

// Remove custom domain
func (s *DomainService) RemoveCustomDomain(ctx context.Context, hostnameID string) error {
    _, err := s.cfClient.CustomHostnames.Delete(ctx, hostnameID, custom_hostnames.CustomHostnameDeleteParams{
        ZoneID: cloudflare.F(s.zoneID),
    })
    return err
}
```

### Configuration Requirements
```yaml
# Janus platform config
cloudflare:
  api_token: "${CF_API_TOKEN}"     # Stored in secrets manager
  zone_id: "${CF_ZONE_ID}"         # Zone for janus.example.com
  platform_domain: "janus.example.com"
  subdomain_suffix: "apps.janus.example.com"
  cname_target: "customers.janus.example.com"
  fallback_origin: "proxy-fallback.janus.example.com"
```

### One-Time Cloudflare Setup Checklist
1. [ ] Add `janus.example.com` zone to Cloudflare
2. [ ] Enable Cloudflare for SaaS on the zone
3. [ ] Create A record: `proxy-fallback.janus.example.com → <server_ip>` (Proxied)
4. [ ] Create A record: `lb.janus.example.com → <lb_ip>` (Proxied)
5. [ ] Create CNAME: `*.apps.janus.example.com → lb.janus.example.com` (Proxied)
6. [ ] Create CNAME: `*.customers.janus.example.com → proxy-fallback.janus.example.com` (Proxied)
7. [ ] Designate `proxy-fallback.janus.example.com` as fallback origin
8. [ ] Create API token with DNS Write + SSL and Certificates Write on the zone
9. [ ] Store API token and zone ID in secrets management

---

## Key Sources
- Cloudflare API v4 DNS Records: https://developers.cloudflare.com/api/resources/dns/subresources/records/
- Cloudflare API Go SDK: https://developers.cloudflare.com/api/go/resources/dns/subresources/records/methods/create/
- cloudflare-go GitHub: https://github.com/cloudflare/cloudflare-go (v6.8.0)
- Cloudflare for SaaS: https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/
- Cloudflare for SaaS Getting Started: https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/getting-started/
- Hostname Validation: https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/domain-support/hostname-validation/
- Certificate Validation: https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/security/certificate-management/issue-and-validate/validate-certificates/
- API Rate Limits: https://developers.cloudflare.com/fundamentals/api/reference/limits/
- API Token Permissions: https://developers.cloudflare.com/fundamentals/api/reference/permissions/
- Plans & Pricing: https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/plans/
- Vercel Custom Domains: https://vercel.com/platforms/docs/multi-tenant-platforms/configuring-domains
