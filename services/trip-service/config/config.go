package config

import (
	"strings"

	"uber/shared/pkg/env"
)

type Config struct {
	JWTSecret      string
	DatabaseURL    string
	RedisAddr      string
	KafkaBrokers   []string
	Port           string
	AllowedOrigins []string // WebSocket CORS allowlist; empty = allow all (dev only)
	InternalSecret string   // shared secret for service-to-service calls (e.g. /assign)
}

func Load() Config {
	var origins []string
	if raw := env.Get("ALLOWED_ORIGINS", ""); raw != "" {
		origins = strings.Split(raw, ",")
	}
	return Config{
		JWTSecret:      env.Get("JWT_SECRET", ""),
		DatabaseURL:    env.Get("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/trips_db?sslmode=disable"),
		RedisAddr:      env.Get("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers:   strings.Split(env.Get("KAFKA_BROKERS", "localhost:9092"), ","),
		Port:           env.Get("PORT", "8083"),
		AllowedOrigins: origins,
		InternalSecret: env.Get("INTERNAL_SECRET", ""),
	}
}
