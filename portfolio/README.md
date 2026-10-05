# steven3zx.com

An embedded-asset Go portfolio served on `127.0.0.1:8090`, ready to sit behind
the existing Caddy reverse proxy. The page uses an industrial, Factorio-inspired
command-center layout to present Steven's engineering profile and selected work.

## Run

```powershell
go run .
```

Set `PORTFOLIO_ADDR` to override the default listener. `/healthz` provides a
plain-text health check. The Go binary embeds the HTML, CSS, and JavaScript.

## Publish through Caddy

Add this site block to the host's private `CADDY/Caddyfile`, then validate and
reload the running Caddy service using its installed configuration:

```caddyfile
www.steven3zx.com {
    reverse_proxy 127.0.0.1:8090
}

steven3zx.com {
    redir https://www.steven3zx.com{uri} permanent
}
```

The repository intentionally does not contain Caddy's local config, certificate
state, or Cloudflare credentials. Ensure Cloudflare DNS for `www` and the apex
routes to the host using the existing arrangement before exposing the service.

Contact address and selected project links in `web/index.html` are starter values
and should be personalized before announcing the portfolio.
