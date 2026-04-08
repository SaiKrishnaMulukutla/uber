# syntax=docker/dockerfile:1

# ── base: all source copied once, module cache lives on host ──
FROM golang:1.21-alpine AS base
RUN apk add --no-cache git ca-certificates
WORKDIR /workspace
ENV GONOSUMDB=*
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download all

# ── shared runtime base: one layer reused by all services ─────
FROM alpine:3.19 AS runtime-base
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app

# ── per-service build stages ──────────────────────────────────
FROM base AS build-user
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out ./services/user-service/cmd

FROM base AS build-driver
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out ./services/driver-service/cmd

FROM base AS build-trip
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out ./services/trip-service/cmd

FROM base AS build-matching
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out ./services/matching-service/cmd

FROM base AS build-notification
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out ./services/notification-service/cmd

FROM base AS build-payment
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out ./services/payment-service/cmd

# ── final lean runtime images ─────────────────────────────────
FROM runtime-base AS user-service
WORKDIR /app
COPY --from=build-user /out .
USER app
EXPOSE 8081
CMD ["./out"]

FROM runtime-base AS driver-service
WORKDIR /app
COPY --from=build-driver /out .
USER app
EXPOSE 8082
CMD ["./out"]

FROM runtime-base AS trip-service
WORKDIR /app
COPY --from=build-trip /out .
USER app
EXPOSE 8083
CMD ["./out"]

FROM runtime-base AS matching-service
WORKDIR /app
COPY --from=build-matching /out .
USER app
CMD ["./out"]

FROM runtime-base AS notification-service
WORKDIR /app
COPY --from=build-notification /out .
USER app
EXPOSE 8084
CMD ["./out"]

FROM runtime-base AS payment-service
WORKDIR /app
COPY --from=build-payment /out .
USER app
EXPOSE 8085
CMD ["./out"]
