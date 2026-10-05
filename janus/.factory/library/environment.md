# Environment

Environment variables, external dependencies, and setup notes.

**What belongs here:** Required env vars, external API keys/services, dependency quirks, platform-specific notes.
**What does NOT belong here:** Service ports/commands (use `.factory/services.yaml`).

---

## Platform
- Windows 10, AMD Ryzen 5 7600X (6C/12T), 32GB RAM
- PowerShell is the default shell (use `;` not `&&` for chaining commands)
- PowerShell aliases `curl` to `Invoke-WebRequest` — use `curl.exe` for real curl
- On this Windows host, socket bind conflicts may surface as `Only one usage of each socket address...` instead of `address already in use`; port-collision detection/retry code should handle both forms

## Runtime Versions
- Go 1.26.0 (Windows AMD64)
- Node 24.14.0, npm 11.9.0
- Podman 5.7.1 (WSL-based machine, 6 CPUs, 2GB RAM)
- TypeScript 5.8.2
- Playwright 1.58.2

## Environment Variables
Stored in `E:\Janus\.env.local` (gitignored).

### Cloudflare (Milestone 5+)
- `CF_API_TOKEN` — Custom token with DNS Edit, SSL and Certificates Edit, Zone Read
- `CF_ZONE_ID` — Zone ID from Cloudflare dashboard

### Stripe (Milestone 7)
- `STRIPE_SECRET_KEY` — Test mode secret key
- `STRIPE_PUBLISHABLE_KEY` — Test mode publishable key
- `STRIPE_WEBHOOK_SECRET` — Webhook signing secret

### Janus Core
- `JANUS_DATABASE_URL` — PostgreSQL connection string (see podman-compose.yml for default dev values)
- `JANUS_JWT_SECRET` — JWT signing secret
- `JANUS_ENV` — dev/staging/production
- See `E:\Janus\backend\janus-api\internal\config\` for full config loading

## External Dependencies
- PostgreSQL 16 (via podman-compose)
- MinIO (S3-compatible object storage, via podman-compose)
- Cloudflare API (DNS management, SSL for SaaS)
- Stripe API (metered billing)
- GitHub API (repo import, OAuth)
