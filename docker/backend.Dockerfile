FROM golang:1.27.1-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS base
WORKDIR /app
RUN apk add --no-cache git
COPY backend/go.mod backend/go.sum ./
RUN go mod download

FROM base AS dev
RUN go install github.com/air-verse/air@v1.65.3
COPY backend/ .
RUN CGO_ENABLED=0 GOOS=linux go build -o /healthcheck ./cmd/healthcheck
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

FROM base AS build
COPY backend/ .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/healthcheck ./cmd/healthcheck
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/receipt-migrate ./cmd/receipt-migrate
RUN mkdir -p /out/data/uploads && chown -R 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12@sha256:9c346e4be81b5ca7ff31a0d89eaeade58b0f95cfd3baed1f36083ddb47ca3160 AS prod
COPY --from=build /out/server /server
COPY --from=build /out/healthcheck /healthcheck
COPY --from=build /out/receipt-migrate /receipt-migrate
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
