# syntax=docker/dockerfile:1

# h3-go (geo module) requires cgo → build with gcc on alpine, run on alpine
# (musl) so the dynamically-linked binary matches the runtime libc.
#
# Targets:
#   (default)      alpine runtime: api, worker, mcp, migrate, keyctl
#   --target worker  debian runtime with osmium-tool for the OSM ingest
#                  (INGEST_SOURCE=osmium). Alpine has no osmium-tool
#                  package, so the worker is built against glibc instead.
FROM golang:1.26-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/mcp ./cmd/mcp \
 && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/keyctl ./cmd/keyctl

# ---- worker (glibc + osmium-tool) ----------------------------------------
FROM golang:1.26-bookworm AS build-worker
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM debian:bookworm-slim AS worker
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata osmium-tool \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system app && useradd --system --gid app --no-create-home app \
 # Extract cache (raw .pbf + osmium artifacts). Mount a volume here; a fresh
 # named volume inherits this ownership.
 && mkdir -p /data/extracts && chown app:app /data/extracts
COPY --from=build-worker /out/worker /app/worker
USER app
WORKDIR /app
ENV INGEST_SOURCE=osmium \
    INGEST_EXTRACT_DIR=/data/extracts
CMD ["/app/worker"]

# ---- default runtime (alpine) ---------------------------------------------
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S app && adduser -S app -G app
COPY --from=build /out/ /app/
USER app
WORKDIR /app
# Default command; each compose service overrides it via `command:`.
# No ENTRYPOINT — the binaries take no args, so command must BE the binary.
CMD ["/app/api"]
