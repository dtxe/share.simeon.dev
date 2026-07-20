FROM node:26-alpine@sha256:725aeba2364a9b16beae49e180d83bd597dbd0b15c47f1f28875c290bfd255b9 AS base
WORKDIR /app
RUN corepack enable
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

FROM base AS dev
COPY frontend/ .
EXPOSE 5173
CMD ["pnpm", "run", "dev", "--", "--host", "0.0.0.0"]

FROM base AS build
COPY frontend/ .
RUN pnpm run build

FROM caddy:2-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648 AS prod
COPY docker/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv
EXPOSE 8001
