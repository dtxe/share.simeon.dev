FROM node:22-alpine@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2 AS base
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

FROM caddy:2-alpine@sha256:6aeddd44c3078b0f9a35206472a11420648a79c184603ef95957d0a20044cb2b AS prod
COPY docker/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv
EXPOSE 8001
