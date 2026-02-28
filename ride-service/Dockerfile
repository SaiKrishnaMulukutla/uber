# ── Build stage ──
FROM golang:1.21-alpine AS build
RUN apk add --no-cache git
WORKDIR /app
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /ride-service ./cmd

# ── Runtime stage ──
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /ride-service .
EXPOSE 8080
CMD ["./ride-service"]
