# syntax=docker/dockerfile:1

# --- build stage -------------------------------------------------------
FROM golang:1.24-alpine AS builder
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

# CGO disabled: a static binary with no libc dependency, so the runtime
# stage can be distroless/scratch-like alpine with nothing else installed.
# -race is intentionally NOT part of this build (it requires cgo and adds
# major runtime overhead); race detection runs in CI/tests, not prod images.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/loadtest-client ./cmd/loadtest-client

# --- runtime stage -------------------------------------------------------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && \
    adduser -D -H -u 10001 appuser
WORKDIR /app
COPY --from=builder /out/server /app/server
COPY --from=builder /out/loadtest-client /app/loadtest-client
USER appuser

ENV HTTP_ADDR=:8080
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- http://localhost:8080/healthz || exit 1

ENTRYPOINT ["/app/server"]
