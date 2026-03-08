.PHONY: up down logs build clean \
        build-user build-driver build-trip build-matching build-notification build-payment \
        logs-user logs-driver logs-trip logs-matching logs-gateway logs-notification logs-payment

# ── Docker ──────────────────────────────────────────────────────────────────

up:
	cd infra && docker-compose up -d --build

down:
	cd infra && docker-compose down

clean:
	cd infra && docker-compose down -v --remove-orphans

# ── Per-service logs ─────────────────────────────────────────────────────────

logs:
	cd infra && docker-compose logs -f user-service driver-service trip-service matching-service notification-service payment-service

logs-user:
	cd infra && docker-compose logs -f user-service

logs-driver:
	cd infra && docker-compose logs -f driver-service

logs-trip:
	cd infra && docker-compose logs -f trip-service

logs-matching:
	cd infra && docker-compose logs -f matching-service

logs-gateway:
	cd infra && docker-compose logs -f api-gateway

logs-notification:
	cd infra && docker-compose logs -f notification-service

logs-payment:
	cd infra && docker-compose logs -f payment-service

# ── Local builds (no Docker) ─────────────────────────────────────────────────

build: build-user build-driver build-trip build-matching build-notification build-payment

build-user:
	cd user-service && go build -o bin/user-service ./cmd

build-driver:
	cd driver-service && go build -o bin/driver-service ./cmd

build-trip:
	cd trip-service && go build -o bin/trip-service ./cmd

build-matching:
	cd matching-service && go build -o bin/matching-service ./cmd

build-notification:
	cd notification-service && go build -o bin/notification-service ./cmd

build-payment:
	cd payment-service && go build -o bin/payment-service ./cmd
