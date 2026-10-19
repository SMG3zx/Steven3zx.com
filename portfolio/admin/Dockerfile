FROM node:26-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27 AS server
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/adminserver . && mkdir -p /out/data

# The Turso driver loads its embedded engine with dlopen, so the runtime needs
# glibc and libgcc (distroless/cc), not the fully static image.
FROM gcr.io/distroless/cc-debian12:nonroot
WORKDIR /app
COPY --from=server /out/adminserver ./adminserver
COPY --from=web /web/dist ./web
COPY --from=server --chown=nonroot:nonroot /out/data /data
ENV ADMIN_DB=/data/admin.db \
    ADMIN_WEB_DIR=/app/web \
    ADMIN_ADDR=:8091 \
    TURSO_GO_CACHE_DIR=/tmp/turso
EXPOSE 8091
ENTRYPOINT ["/app/adminserver"]
