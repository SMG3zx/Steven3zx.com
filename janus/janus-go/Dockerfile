# The janus binary, for `janus serve <service>` in a container each. Wasmtime
# needs cgo, so the build stage has a C toolchain and the runtime stage the C
# library.
FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -o /out/janus ./cmd/janus

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --home-dir /data janus \
    && mkdir /data \
    && chown janus /data
COPY --from=build /out/janus /usr/local/bin/janus
USER janus
WORKDIR /data
ENTRYPOINT ["janus"]

# The builds service compiles in containers of its own, so it needs the Docker
# CLI and, at run time, the host's Docker socket.
FROM runtime AS builds
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends docker.io \
    && rm -rf /var/lib/apt/lists/*
