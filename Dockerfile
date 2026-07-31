# syntax=docker/dockerfile:1
#
# Samo server image: a single static Go binary plus the ffmpeg/fpcalc tools the
# scanner and explo pipeline shell out to. Nothing else is needed at runtime —
# the web UI and migrations are embedded in the binary.

# ---- build stage -----------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# Cache module downloads across source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Pure-Go dependencies (pgx) mean CGO can stay off, so the binary is fully
# static and runs on the slim runtime image unchanged.
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags "-s -w" -o /out/samo-server ./cmd/samo-server

# ---- runtime stage ---------------------------------------------------------
FROM debian:bookworm-slim

# ffmpeg + ffprobe for transcoding/probing; fpcalc (libchromaprint-tools) for
# the explo audio fingerprinting; ca-certificates for HTTPS to podcast feeds
# and metadata providers.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ffmpeg \
        libchromaprint-tools \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/samo-server /usr/local/bin/samo-server

# Run as an unprivileged user; /data is the single persisted directory.
RUN useradd --system --uid 10001 --create-home --home-dir /home/samo samo \
    && mkdir -p /data \
    && chown -R samo:samo /data

ENV SAMO_DATA_DIR=/data \
    SAMO_ADDR=:6969 \
    SAMO_FFMPEG_PATH=/usr/bin/ffmpeg \
    SAMO_FFPROBE_PATH=/usr/bin/ffprobe \
    SAMO_FPCALC_PATH=/usr/bin/fpcalc

USER samo
WORKDIR /data
EXPOSE 6969

# The binary probes itself, so the slim runtime needs no curl/wget. /health
# returns 503 when Postgres is unreachable, which is what makes this catch a
# server that is running but unable to serve anything.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD ["/usr/local/bin/samo-server", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/samo-server"]
