FROM golang:1.25.11-alpine@sha256:523c3effe300580ed375e43f43b1c9b091b68e935a7c3a92bfcc4e7ed55b18c2 AS base
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

FROM gcr.io/distroless/static-debian12@sha256:a9fcaedd4c9b59e12dd65d954f0b5044f19b0647a8a3712e77205df9e7b102cd AS prod
COPY --from=build /out/server /server
COPY --from=build /out/healthcheck /healthcheck
COPY --from=build /out/receipt-migrate /receipt-migrate
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
