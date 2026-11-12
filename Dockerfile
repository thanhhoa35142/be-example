# syntax=docker/dockerfile:1

# h3-go (geo module) requires cgo → build with gcc on alpine, run on alpine
# (musl) so the dynamically-linked binary matches the runtime libc.
FROM golang:1.25-alpine AS build
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

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S app && adduser -S app -G app
COPY --from=build /out/ /app/
USER app
WORKDIR /app
# Default command; each compose service overrides it via `command:`.
# No ENTRYPOINT — the binaries take no args, so command must BE the binary.
CMD ["/app/api"]
