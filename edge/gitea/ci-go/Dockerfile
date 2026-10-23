# CI job image for Gitea Actions: Go 1.27 plus Bun. The runner runs JavaScript
# actions such as actions/checkout with a `node` binary, and Bun stands in for it,
# the same way oven/bun provides its node fallback. Build it on the runner host:
#   docker build -t steven3zx-ci-go:1.27 edge/gitea/ci-go
FROM golang:1.27-bookworm

COPY --from=oven/bun:1 /usr/local/bin/bun /usr/local/bin/bun
RUN ln -s /usr/local/bin/bun /usr/local/bin/bunx \
 && ln -s /usr/local/bin/bun /usr/local/bin/node \
 && bun --version && go version
