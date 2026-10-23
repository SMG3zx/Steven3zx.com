# CI job image for Gitea Actions: Go 1.27 plus Node 26. The runner needs node to run
# JavaScript actions such as actions/checkout, and golang:1.27 does not include it.
# Build it on the runner host: docker build -t steven3zx-ci-go:1.27 edge/gitea/ci-go
FROM golang:1.27-bookworm

COPY --from=node:26-bookworm /usr/local/bin/node /usr/local/bin/node
COPY --from=node:26-bookworm /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -sf /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
 && ln -sf /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
 && node --version && go version
