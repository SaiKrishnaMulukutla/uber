.PHONY: up down logs build clean

up:
	cd infra && docker-compose up -d --build

down:
	cd infra && docker-compose down

logs:
	cd infra && docker-compose logs -f ride-service

build:
	cd ride-service && go build -o bin/ride-service ./cmd

clean:
	cd infra && docker-compose down -v --remove-orphans
