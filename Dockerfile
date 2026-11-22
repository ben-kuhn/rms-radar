# syntax=docker/dockerfile:1
FROM golang:1.27-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o /out/rms-radar .

FROM debian:trixie-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      tzdata \
      voacapl \
      voacapl-data \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --uid 65532 --home-dir /data --shell /usr/sbin/nologin rms-radar \
 && install -d -o 65532 -g 65532 /data

COPY --from=build /out/rms-radar /usr/local/bin/rms-radar

ENV RMSRADAR_CACHE_DIR=/data \
    RMSRADAR_LISTEN=0.0.0.0:8079 \
    RMSRADAR_REFRESH=24h \
    RMSRADAR_SERVICE_CODES=PUBLIC,MERCURY,VARDEFM,MODEM73,DATA2G,IONHF,XARA \
    RMSRADAR_VOACAP_EXECUTABLE=/usr/bin/voacapl \
    RMSRADAR_VOACAP_DATA_DIR=/usr/share/voacapl/itshfbc

VOLUME ["/data"]
EXPOSE 8079

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD ["curl", "-fsS", "http://127.0.0.1:8079/api/health"]

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/rms-radar"]
CMD ["-d"]
