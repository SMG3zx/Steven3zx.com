FROM oven/bun:1

WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
RUN bun add --global @openai/codex@0.155.1
COPY . .

EXPOSE 4781
CMD ["bun", "run", "dev"]
