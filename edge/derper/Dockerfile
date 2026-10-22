# derper, Tailscale's reference DERP and STUN relay, built from the pinned module.
FROM golang:1.27-bookworm AS build
RUN GOBIN=/out go install tailscale.com/cmd/derper@v1.104.2

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/derper /usr/local/bin/derper
ENTRYPOINT ["/usr/local/bin/derper"]
