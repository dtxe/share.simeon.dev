FROM golang:1.25.11-alpine@sha256:523c3effe300580ed375e43f43b1c9b091b68e935a7c3a92bfcc4e7ed55b18c2 AS base
WORKDIR /app
RUN apk add --no-cache git
COPY backend/go.mod backend/go.sum ./
RUN go mod download

FROM base AS dev
RUN apk add --no-cache tesseract-ocr tesseract-ocr-data-eng
RUN go install github.com/air-verse/air@v1.65.3
COPY backend/ .
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

FROM base AS build
COPY backend/ .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server
RUN mkdir -p /out/data/uploads && chown -R 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12@sha256:9c346e4be81b5ca7ff31a0d89eaeade58b0f95cfd3baed1f36083ddb47ca3160 AS prod
COPY --from=build /out/server /server
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
