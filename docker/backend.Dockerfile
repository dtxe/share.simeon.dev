FROM golang:1.25-alpine AS base
WORKDIR /app
RUN apk add --no-cache git
COPY backend/go.mod backend/go.sum ./
RUN go mod download

FROM base AS dev
RUN go install github.com/air-verse/air@latest
COPY backend/ .
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

FROM base AS build
COPY backend/ .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12 AS prod
COPY --from=build /out/server /server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
