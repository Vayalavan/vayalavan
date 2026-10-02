# Build and run one Go service.
#
# Shared by all the Go services via a build ARG rather than four near-identical
# files that drift apart. Built from the REPO ROOT as context, because go.work
# and packages/vm-go-common live above each service directory:
#
#   docker build -f vm-profile-api/Dockerfile --build-arg SERVICE=vm-profile-api .

# ---- build ----------------------------------------------------------------
FROM golang:1.26-alpine AS build

ARG SERVICE
RUN test -n "$SERVICE" || (echo "SERVICE build-arg is required" && exit 1)

WORKDIR /src

# Dependency manifests first, so a code-only change reuses the module cache.
COPY go.work go.work.sum* ./
COPY packages/vm-go-common/go.mod packages/vm-go-common/go.sum ./packages/vm-go-common/
COPY vm-profile-api/go.mod vm-profile-api/go.sum ./vm-profile-api/
COPY vm-catalog-api/go.mod vm-catalog-api/go.sum ./vm-catalog-api/
COPY vm-orders-api/go.mod vm-orders-api/go.sum ./vm-orders-api/
COPY vm-events-consumer-api/go.mod vm-events-consumer-api/go.sum ./vm-events-consumer-api/
RUN go mod download all

COPY packages/vm-go-common ./packages/vm-go-common
COPY ${SERVICE} ./${SERVICE}

# CGO off for a static binary that runs in a scratch-like image. Symbols and
# DWARF stripped: smaller image, and stack traces still carry function names.
# Timezone data is embedded by vm-go-common/isttime, so no tzdata package is
# needed at runtime — IST arithmetic must not depend on the host image.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath -ldflags="-s -w" \
        -o /out/service ./${SERVICE}/cmd/api

# ---- run ------------------------------------------------------------------
FROM alpine:3.21

# ca-certificates for outbound TLS (Razorpay, S3, SMTP). wget is already in
# busybox and is what the healthcheck uses.
RUN apk add --no-cache ca-certificates \
    && addgroup -S vayal && adduser -S -G vayal vayal

COPY --from=build /out/service /usr/local/bin/service

# Non-root. A container escape should not start from uid 0.
USER vayal

# Documentation only; the actual port comes from PORT.
EXPOSE 8080

# Liveness is the orchestrator's job, but a healthcheck here means `docker
# compose ps` tells the truth during a manual deploy too.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${PORT:-8080}/healthz" || exit 1

ENTRYPOINT ["/usr/local/bin/service"]
