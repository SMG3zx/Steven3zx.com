# Stripe Metered Billing Research Report for Janus

## 1. Stripe Meters API — Modern Usage-Based Billing

Stripe's **Meters API** (v1/v2) is the current recommended approach for usage-based billing, replacing the legacy `usage_records` on subscription items. Key concepts:

### Core Objects

| Stripe Object | Purpose |
|---|---|
| **Meter** (`billing.meter`) | Defines how to aggregate usage events over a billing period. Has an `event_name`, aggregation formula (sum/count/last), and customer mapping. |
| **Meter Event** (`billing.meter_event`) | Individual usage record. Contains `event_name`, `payload` (with `stripe_customer_id` and `value`), and optional `timestamp`. |
| **Product** | Represents the service (e.g., "Janus Compute") |
| **Price** | Defines unit cost, currency, billing period. Linked to a Meter for usage-based pricing. Supports per-unit, per-package, and tiered pricing. |
| **Subscription** | Associates a Customer with Price(s). For metered billing, the first invoice has no usage; usage is billed in arrears. |
| **Customer** | The end user being billed. |

### How It Works

1. **Create Meters** — one per billable dimension (CPU, RAM, disk, network)
2. **Create Products & Prices** — link each Price to its corresponding Meter
3. **Create Subscription** — subscribe customer to all metered prices
4. **Report Usage** — send Meter Events via API throughout billing period
5. **Invoice** — Stripe automatically calculates usage at period end and charges

### Meter Configuration

```
Meter object:
{
  "display_name": "CPU Minutes",
  "event_name": "janus_cpu_minutes",
  "default_aggregation": { "formula": "sum" },
  "customer_mapping": {
    "type": "by_id",
    "event_payload_key": "stripe_customer_id"
  },
  "value_settings": {
    "event_payload_key": "value"
  }
}
```

---

## 2. Recommended Janus Billing Architecture

### 2.1 Meters (4 separate meters)

| Meter Name | Event Name | Unit | Aggregation |
|---|---|---|---|
| CPU Minutes | `janus_cpu_minutes` | core-minutes | sum |
| RAM Minutes | `janus_ram_gb_minutes` | GB-minutes | sum |
| Disk Storage | `janus_disk_gb_hours` | GB-hours | sum |
| Network Egress | `janus_network_egress_gb` | GB transferred | sum |

### 2.2 Products & Prices

Create one **Product** per resource dimension, each with a metered **Price**:

| Product | Price (per unit) | Unit |
|---|---|---|
| Janus CPU | $0.001 per core-minute | core-minute |
| Janus RAM | $0.0003 per GB-minute | GB-minute |
| Janus Disk | $0.00008 per GB-hour | GB-hour |
| Janus Network | $0.09 per GB | GB egress |

*Prices above are suggested starting points — see Section 5 for derivation.*

### 2.3 Subscription Model

**Recommended: Subscription with metered usage items (pay-as-you-go)**

- One subscription per customer with multiple `subscription_items`, one per metered price
- Use `billing_mode=flexible` (new) — Stripe skips the first invoice since no usage exists yet
- Billing period: **Monthly**
- Collection method: `charge_automatically` with card on file
- Optional: Add a flat monthly "platform fee" price item (e.g., $0/month free tier or $5/month base)

**Alternative: Prepaid Credits Model**
- Stripe Billing Credits (public preview) allow granting credits that burn down with usage
- Good for "buy $50 in credits, use until depleted" model
- More complex; better suited for later phase

### 2.4 Usage Reporting Strategy

**Batch reporting every 1-5 minutes** is the recommended approach:

- Your backend aggregates container metrics locally (CPU-seconds, RAM-seconds, bytes)
- Every 1-5 minutes, convert to billing units and send to Stripe
- Use **idempotency keys** (identifier field) to prevent duplicate billing
- Stripe processes events asynchronously; upcoming invoices reflect usage after a short delay

**Rate Limits:**
- v1 API: 1,000 meter events/second in live mode
- v2 Meter Event Streams: 10,000 events/second (for high throughput)
- For Janus's scale, v1 API (1,000/s) is more than sufficient

**Timestamp Rules:**
- Must be within past 35 calendar days
- No more than 5 minutes in the future
- Values must be whole numbers (integers only)

---

## 3. Go SDK Usage Patterns

### 3.1 Installation

```bash
go get github.com/stripe/stripe-go/v84
```

The latest major version is **v84**. Import as:
```go
import (
    "github.com/stripe/stripe-go/v84"
    "github.com/stripe/stripe-go/v84/billing/meter"
    "github.com/stripe/stripe-go/v84/billing/meterevent"
    "github.com/stripe/stripe-go/v84/customer"
    "github.com/stripe/stripe-go/v84/price"
    "github.com/stripe/stripe-go/v84/product"
    "github.com/stripe/stripe-go/v84/subscription"
)
```

### 3.2 Initialize Client

```go
import "github.com/stripe/stripe-go/v84/client"

sc := client.New("sk_live_...", nil)
```

Or set the global key:
```go
stripe.Key = "sk_live_..."
```

### 3.3 Create a Meter

```go
params := &stripe.BillingMeterParams{
    DisplayName: stripe.String("CPU Minutes"),
    EventName:   stripe.String("janus_cpu_minutes"),
    DefaultAggregation: &stripe.BillingMeterDefaultAggregationParams{
        Formula: stripe.String("sum"),
    },
    CustomerMapping: &stripe.BillingMeterCustomerMappingParams{
        Type:            stripe.String("by_id"),
        EventPayloadKey: stripe.String("stripe_customer_id"),
    },
    ValueSettings: &stripe.BillingMeterValueSettingsParams{
        EventPayloadKey: stripe.String("value"),
    },
}
m, err := meter.New(params)
```

### 3.4 Create Product & Metered Price

```go
// Create product
prodParams := &stripe.ProductParams{
    Name: stripe.String("Janus CPU"),
}
prod, err := product.New(prodParams)

// Create metered price linked to meter
priceParams := &stripe.PriceParams{
    Product:    stripe.String(prod.ID),
    Currency:   stripe.String(string(stripe.CurrencyUSD)),
    UnitAmount: stripe.Int64(0), // Use unit_amount_decimal for sub-cent
    UnitAmountDecimal: stripe.Float64(0.1), // $0.001 = 0.1 cents
    Recurring: &stripe.PriceRecurringParams{
        Interval:  stripe.String(string(stripe.PriceRecurringIntervalMonth)),
        UsageType: stripe.String("metered"),
        Meter:     stripe.String(m.ID), // Link to meter
    },
    BillingScheme: stripe.String("per_unit"),
}
pr, err := price.New(priceParams)
```

### 3.5 Create Customer

```go
custParams := &stripe.CustomerParams{
    Email: stripe.String("user@example.com"),
    Name:  stripe.String("Jane Doe"),
    Metadata: map[string]string{
        "janus_user_id": "usr_abc123",
    },
}
cust, err := customer.New(custParams)
```

### 3.6 Create Subscription with Multiple Metered Items

```go
subParams := &stripe.SubscriptionParams{
    Customer: stripe.String(cust.ID),
    Items: []*stripe.SubscriptionItemsParams{
        {Price: stripe.String(cpuPriceID)},
        {Price: stripe.String(ramPriceID)},
        {Price: stripe.String(diskPriceID)},
        {Price: stripe.String(networkPriceID)},
    },
}
sub, err := subscription.New(subParams)
```

### 3.7 Report Usage (Meter Events)

```go
import "github.com/stripe/stripe-go/v84/billing/meterevent"

// Report CPU usage for a customer
eventParams := &stripe.BillingMeterEventParams{
    EventName: stripe.String("janus_cpu_minutes"),
    Payload: map[string]string{
        "stripe_customer_id": customerID,
        "value":              "150", // 150 core-minutes
    },
    Identifier: stripe.String("cpu-usage-" + stackID + "-" + timestamp), // idempotency
    Timestamp:  stripe.Int64(time.Now().Unix()),
}
event, err := meterevent.New(eventParams)
```

### 3.8 High-Throughput Meter Event Streams (v2 API)

For very high volume (>1000 events/sec), use the v2 meter event stream:

```go
// 1. Create a meter event session (get auth token)
// 2. Use the token to stream events at up to 10,000/sec
// See: github.com/stripe/stripe-go/v84/example/v2/meter_event_stream
```

The v2 API uses stateless auth sessions (tokens valid for 15 minutes). For Janus's expected scale, the standard v1 endpoint (1,000/sec) should be sufficient.

### 3.9 Webhook Handler

```go
import (
    "encoding/json"
    "io"
    "net/http"
    "github.com/stripe/stripe-go/v84"
    "github.com/stripe/stripe-go/v84/webhook"
)

func handleWebhook(w http.ResponseWriter, req *http.Request) {
    body, err := io.ReadAll(req.Body)
    if err != nil {
        w.WriteHeader(http.StatusBadRequest)
        return
    }

    event, err := webhook.ConstructEvent(body, 
        req.Header.Get("Stripe-Signature"), 
        webhookSecret)
    if err != nil {
        w.WriteHeader(http.StatusBadRequest)
        return
    }

    switch event.Type {
    case "invoice.paid":
        // Subscription renewed successfully; ensure access continues
        var invoice stripe.Invoice
        json.Unmarshal(event.Data.Raw, &invoice)
        // Update user's billing status

    case "invoice.payment_failed":
        // Payment failed; notify user, consider pausing resources
        var invoice stripe.Invoice
        json.Unmarshal(event.Data.Raw, &invoice)
        // Flag user account, send notification

    case "customer.subscription.deleted":
        // Subscription canceled; revoke compute access
        var sub stripe.Subscription
        json.Unmarshal(event.Data.Raw, &sub)
        // Shut down user's stacks

    case "customer.subscription.updated":
        // Subscription changed (e.g., plan upgrade/downgrade)
        var sub stripe.Subscription
        json.Unmarshal(event.Data.Raw, &sub)
        // Handle status transitions

    case "invoice.upcoming":
        // Invoice coming soon; opportunity to add line items

    case "invoice.finalization_failed":
        // Invoice couldn't be finalized; investigate and retry
    }

    w.WriteHeader(http.StatusOK)
}
```

---

## 4. Webhook Events to Handle

### Critical Events

| Event | Action |
|---|---|
| `invoice.paid` | Confirm payment success, maintain user access |
| `invoice.payment_failed` | Notify user, enable smart retries, consider pausing stacks |
| `invoice.payment_action_required` | Notify user that authentication is needed |
| `customer.subscription.created` | Provision user's environment |
| `customer.subscription.deleted` | Revoke access, terminate stacks |
| `customer.subscription.updated` | Handle status changes (active→past_due, etc.) |

### Meter-Specific Events

| Event | Action |
|---|---|
| `v1.billing.meter.error_report_triggered` | Invalid meter events detected; inspect and resend |
| `v1.billing.meter.no_meter_found` | Event sent with wrong meter name; fix and resend |

### Important Events

| Event | Action |
|---|---|
| `invoice.upcoming` | Sent days before renewal; add extra items if needed |
| `invoice.created` | Invoice created; ensure finalization happens |
| `invoice.finalization_failed` | Invoice can't be finalized; investigate tax/address issues |
| `customer.subscription.trial_will_end` | Trial ending in 3 days; ensure payment method exists |

---

## 5. Cloud Provider Pricing Research (2025-2026)

### 5.1 CPU Pricing (per core-hour, on-demand)

| Provider | Instance | vCPU | RAM | $/hour | $/core-hour | $/core-minute |
|---|---|---|---|---|---|---|
| AWS | m5.large | 2 | 8 GB | $0.096 | $0.048 | $0.0008 |
| Azure | D2s_v3 | 2 | 8 GB | $0.096 | $0.048 | $0.0008 |
| GCP | n2-standard-2 | 2 | 8 GB | $0.0985 | $0.049 | $0.00082 |
| Oracle | E4.Flex | 2 | 8 GB | $0.038 | $0.019 | $0.00032 |
| **Average** | | | | | **$0.041** | **$0.00068** |

### 5.2 RAM Pricing (per GB-hour, derived from instance pricing)

RAM cost is typically bundled with CPU. Approximate isolated cost (from flexible/custom instances):

| Provider | $/GB-hour | $/GB-minute |
|---|---|---|
| AWS (m5) | ~$0.012 | ~$0.0002 |
| GCP (custom) | ~$0.0067 | ~$0.00011 |
| Oracle (Flex) | ~$0.0015 | ~$0.000025 |
| **Average** | **~$0.007** | **~$0.00012** |

### 5.3 Disk Storage Pricing (per GB-month)

| Provider | Type | $/GB-month | $/GB-hour |
|---|---|---|---|
| AWS EBS gp3 | SSD | $0.08 | $0.00011 |
| Azure Premium SSD v2 | SSD | $0.081 | $0.00011 |
| GCP Persistent Disk | SSD | $0.17 | $0.00023 |
| Oracle Block Volume | SSD | $0.0255 | $0.000035 |
| **Average** | | **$0.089** | **$0.00012** |

### 5.4 Network Egress Pricing (per GB)

| Provider | First 10 TB/month |
|---|---|
| AWS | $0.09/GB |
| Azure | $0.08/GB (after free 100 GB) |
| GCP | $0.085/GB (after free 200 GB) |
| Oracle | Free first 10 TB, then $0.0085/GB |
| **Average (AWS/Azure/GCP)** | **$0.085/GB** |

---

## 6. Recommended Janus Pricing Model

### 6.1 Strategy: Cost + Margin

For a small platform, use **2-3x markup** over cloud provider cost to cover:
- Platform overhead (orchestration, monitoring, CI/CD)
- Stripe fees (2.9% + $0.30 per transaction)
- Engineering/operational costs
- Margin/profit

### 6.2 Recommended Prices

| Resource | Your Cost (approx) | Janus Price | Markup |
|---|---|---|---|
| CPU | $0.0008/core-min | **$0.002/core-minute** | 2.5x |
| RAM | $0.0002/GB-min | **$0.0005/GB-minute** | 2.5x |
| Disk | $0.00011/GB-hour | **$0.0003/GB-hour** (~$0.22/GB-month) | 2.7x |
| Network Egress | $0.085/GB | **$0.12/GB** | 1.4x |

### 6.3 Example Monthly Bill

A user running a 2-core, 4GB RAM container 24/7 for a month:

| Resource | Usage | Rate | Cost |
|---|---|---|---|
| CPU | 2 cores × 43,200 min = 86,400 core-min | $0.002 | $172.80 |
| RAM | 4 GB × 43,200 min = 172,800 GB-min | $0.0005 | $86.40 |
| Disk | 20 GB × 720 hrs = 14,400 GB-hrs | $0.0003 | $4.32 |
| Network | 50 GB | $0.12 | $6.00 |
| **Total** | | | **$269.52** |

For comparison, equivalent AWS m5.large (2 vCPU, 8GB) = ~$69/month on-demand. The premium reflects the platform value-add (managed orchestration, one-click deploys, WASM artifact builds, etc.).

### 6.4 Alternative: Simplified "Compute Minutes" Model

Instead of 4 separate meters, use a **single "compute-minute" unit** that bundles CPU+RAM:

| Tier | Specs | Price |
|---|---|---|
| Small | 1 core, 2 GB RAM | $0.003/minute ($2.16/day) |
| Medium | 2 cores, 4 GB RAM | $0.006/minute ($4.32/day) |
| Large | 4 cores, 8 GB RAM | $0.012/minute ($8.64/day) |

This is simpler for users to understand and requires only 1-2 meters (compute + network).

---

## 7. Required Stripe Objects Summary

### Setup Phase (one-time)

```
4 Meters:
  - janus_cpu_minutes (sum aggregation)
  - janus_ram_gb_minutes (sum aggregation)
  - janus_disk_gb_hours (sum aggregation)
  - janus_network_egress_gb (sum aggregation)

4 Products:
  - Janus CPU Compute
  - Janus RAM
  - Janus Disk Storage
  - Janus Network Egress

4 Prices (one per product, linked to corresponding meter):
  - CPU: $0.002/core-minute, monthly, metered, linked to janus_cpu_minutes
  - RAM: $0.0005/GB-minute, monthly, metered, linked to janus_ram_gb_minutes
  - Disk: $0.0003/GB-hour, monthly, metered, linked to janus_disk_gb_hours
  - Network: $0.12/GB, monthly, metered, linked to janus_network_egress_gb
```

### Per-Customer

```
1 Customer object (with metadata mapping to Janus user ID)
1 Subscription (with 4 subscription items, one per metered price)
N Meter Events (reported continuously throughout billing period)
```

### Webhook Endpoint

```
1 Webhook endpoint registered to receive:
  - invoice.paid
  - invoice.payment_failed
  - invoice.payment_action_required
  - invoice.upcoming
  - invoice.finalization_failed
  - customer.subscription.created
  - customer.subscription.updated
  - customer.subscription.deleted
  - v1.billing.meter.error_report_triggered
  - v1.billing.meter.no_meter_found
```

---

## 8. Customer Portal

Stripe provides a **hosted Customer Portal** for users to:
- View current subscription and usage
- Update payment methods
- View invoice history
- Cancel subscription

Create a portal session in Go:
```go
import "github.com/stripe/stripe-go/v84/billingportal/session"

params := &stripe.BillingPortalSessionParams{
    Customer:  stripe.String(customerID),
    ReturnURL: stripe.String("https://app.janus.dev/billing"),
}
s, err := session.New(params)
// Redirect user to s.URL
```

---

## 9. Implementation Phases

### Phase 1: Basic Metered Billing
- Create meters, products, prices in Stripe (can do via Dashboard first)
- Implement customer creation + subscription on user signup
- Add usage reporting from backend (batch every 5 min)
- Implement webhook handler for payment events
- Add Customer Portal link

### Phase 2: Usage Dashboard
- Use Stripe's Meter Usage Analytics API to show usage to users
- Build a usage dashboard in the Janus frontend
- Add billing alerts (usage thresholds)

### Phase 3: Advanced Features
- Billing credits / prepaid model
- Volume discounts (tiered pricing)
- Free tier / trial periods
- Invoice customization

---

## Sources

- Stripe Meters API: https://docs.stripe.com/api/billing/meter
- Stripe Meter Events API: https://docs.stripe.com/api/billing/meter-event
- Usage-based billing overview: https://docs.stripe.com/billing/subscriptions/usage-based
- Pay-as-you-go implementation guide: https://docs.stripe.com/billing/subscriptions/usage-based/implementation-guide
- Recording usage with API: https://docs.stripe.com/billing/subscriptions/usage-based/recording-usage-api
- Subscription webhooks: https://docs.stripe.com/billing/subscriptions/webhooks
- Advanced pricing plans: https://docs.stripe.com/billing/subscriptions/usage-based/pricing-plans
- stripe-go SDK: https://github.com/stripe/stripe-go (v84)
- Cloud pricing comparison: https://www.emma.ms/blog/cloud-pricing-comparison-compute-storage-and-networking
- AWS EC2 pricing: https://aws.amazon.com/ec2/pricing/on-demand/
