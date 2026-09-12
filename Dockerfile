# syntax=docker/dockerfile:1

# ---- build -----------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

WORKDIR /src

# Dependencies first, so the module cache survives source-only changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

# The SQLite driver is pure Go, so this is an ordinary cross-compile with no
# C toolchain and no per-architecture emulation.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ift ./cmd/ift

# ---- runtime ---------------------------------------------------------------
FROM alpine:3.24

ARG VERSION=dev

LABEL org.opencontainers.image.title="insta-follower-tracker" \
      org.opencontainers.image.description="Track Instagram followers over time from the official data export" \
      org.opencontainers.image.source="https://github.com/williamokano/insta-follower-tracker" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

# su-exec drops privileges without the signal-forwarding problems of su.
RUN apk add --no-cache ca-certificates su-exec tzdata

COPY --from=build /out/ift /app/ift
COPY docker/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

ENV IFT_DATA_DIR=/data \
    IFT_ADDR=:8080 \
    PUID=1000 \
    PGID=1000

# Declared so the database and uploaded exports survive a container rebuild.
VOLUME ["/data"]
EXPOSE 8080

# The probe keeps wget's output rather than discarding it. Docker records the
# last few probes in "docker inspect --format '{{json .State.Health}}'", and a
# silenced probe leaves nothing there but "exit 1" — which is how this check
# spent several releases resolving 127.0.0.18080 without anyone being able to
# see why the container would not go healthy.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -O- "http://127.0.0.1:${IFT_ADDR#*:}/healthz" || exit 1

ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["/app/ift"]
