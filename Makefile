.PHONY: up rebuild down logs build clean \
        logs-user logs-driver logs-trip logs-matching logs-gateway logs-notification logs-payment logs-otp

export DOCKER_BUILDKIT=1

# ── Docker ───────────────────────────────────────────────────────────────────

up:
	cd infra && docker-compose up -d

rebuild:
	cd infra && docker-compose up -d --build

down:
	cd infra && docker-compose down

clean:
	cd infra && docker-compose down -v --remove-orphans

# ── Per-service logs ──────────────────────────────────────────────────────────

logs:
	cd infra && docker-compose logs -f user-service driver-service trip-service matching-service notification-service payment-service otp-service

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

logs-otp:
	cd infra && docker-compose logs -f otp-service

# ── Local builds (no Docker) ──────────────────────────────────────────────────

build:
	go build uber/...

build-%:
	go build uber/$*/...
