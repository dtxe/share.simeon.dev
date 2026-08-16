FROM golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/image-converter ./cmd/image-converter

FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce
RUN apk add --no-cache \
        imagemagick=7.1.2.15-r0 \
        imagemagick-heic=7.1.2.15-r0 \
        imagemagick-jpeg=7.1.2.15-r0 \
        imagemagick-libs=7.1.2.15-r0 \
        imagemagick-webp=7.1.2.15-r0 \
    && addgroup -S -g 10001 imageconverter \
    && adduser -S -D -H -u 10001 -G imageconverter imageconverter \
    && mkdir -p /tmp \
    && chown imageconverter:imageconverter /tmp
COPY docker/image-converter-policy.xml /etc/ImageMagick-7/policy.xml
COPY --from=build /out/image-converter /image-converter
RUN chmod 0555 /image-converter && chmod 0444 /etc/ImageMagick-7/policy.xml
USER imageconverter:imageconverter
WORKDIR /tmp
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/image-converter"]
