FROM golang:1.21-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /workspace
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out ./services/notification-service/cmd

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /out .
USER app
EXPOSE 8084
CMD ["./out"]
