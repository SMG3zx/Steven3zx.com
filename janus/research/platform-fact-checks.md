# Platform fact checks

Checked on 2026-10-19 for the Janus platform map (Linear ENG-44). Each item says how it was checked and how much to trust it.

## Storage prices (replaces the $0.15/GiB-month placeholder)

| Provider | Storage | Operations | Egress | Source |
|---|---|---|---|---|
| Cloudflare R2 Standard | $0.015 per GB-month | Class A $4.50 per million, Class B $0.36 per million | none | [Cloudflare R2 pricing](https://developers.cloudflare.com/r2/pricing) (official, via search result) |
| Cloudflare R2 Infrequent Access | $0.01 per GB-month plus $0.01/GB retrieval | Class A $9.00 per million, Class B $0.90 per million | none | same |
| Amazon S3 Standard, first 50 TB, us-east-1 | $0.023 per GB-month | billed separately | billed separately | third-party pages quoting the AWS Price List API, not AWS's own page |
| Fly.io volumes | $0.15 per GB-month | none | n/a | Fly pricing docs (from the earlier research) |

R2 Standard includes a free tier of 10 GB-months, 1 million Class A and 10 million Class B requests each month.

The old $0.15 placeholder came from Fly volumes, which are block storage. Janus stores snapshots and artifacts, which are object-shaped, so R2 and S3 are the better comparison. Storage costs roughly 6 to 10 times less than the placeholder.

## Lambda price check

Confirmed on [AWS Lambda pricing](https://aws.amazon.com/lambda/pricing/) (fetched directly): $0.0000166667 per GB-second, $0.20 per million requests, 1 million requests and 400,000 GB-seconds free each month. The page sends data transfer out to the EC2 on-demand rates and does not state them. The $0.09/GB first-tier egress figure is therefore still third-party.

## Still unverified

- **Google Cloud Run:** Google's page could not be read by the fetch tool (it returned only the title). The earlier figures stand as third-party and search-snippet figures.
- **Azure Container Apps:** the per-second CPU and memory rates were never retrieved. Only the free grant and the bandwidth page figures are known.
- **Cloudflare Workers, Fastly Compute, Fly egress:** unchanged from the earlier research comment on ENG-57.

## Headscale pre-auth key request

Not confirmed. The Headscale docs say the REST API uses a Bearer API key created with `headscale apikeys create`, and that the schema is served by each server at `/swagger`. The docs do not show the pre-auth key request body. From the CLI and server code, the request carries a user, a reusable flag, an ephemeral flag, an expiration, and a list of tags that must each start with `tag:`. Field names may differ between releases.

**To settle it:** run the Headscale version we deploy and read its `/swagger` page. Do this when the enrollment spec is written, against the pinned version.

## Fuel-to-CPU-second constant

Not measured. The current Wasmtime runtime does not record fuel, so the measurement needs the metering change from the ENG-55 decision first.
