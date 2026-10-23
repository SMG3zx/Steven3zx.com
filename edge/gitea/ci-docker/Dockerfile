# CI job image for the deploy runner: the Docker CLI and Compose, which talk to the
# host engine through the mounted socket, plus git and Bun as node for actions such
# as actions/checkout. Build it on the runner host:
#   docker build -t steven3zx-ci-docker:1 edge/gitea/ci-docker
FROM docker:cli

RUN apk add --no-cache git docker-cli-compose libstdc++ libgcc
COPY --from=oven/bun:1-alpine /usr/local/bin/bun /usr/local/bin/bun
RUN ln -s /usr/local/bin/bun /usr/local/bin/bunx \
 && ln -s /usr/local/bin/bun /usr/local/bin/node \
 && bun --version && git --version && docker compose version
