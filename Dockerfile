# syntax=docker/dockerfile:1.7
FROM golang:1.26.5-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.builtAt=${BUILD_DATE}" \
    -o /out/acebridge ./cmd/acebridge && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="AceBridge" \
      org.opencontainers.image.description="Gestor local de canales AceStream y proxy HLS para Jellyfin" \
      org.opencontainers.image.source="https://github.com/anubisreal/acebridge" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/acebridge /acebridge
COPY --chown=nonroot:nonroot --from=build /out/data /data
VOLUME ["/data"]
EXPOSE 8080
ENV ACEBRIDGE_DATA_DIR=/data ACEBRIDGE_LISTEN_ADDR=:8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/acebridge", "healthcheck"]
ENTRYPOINT ["/acebridge"]
