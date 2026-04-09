package config

import (
	"strings"

	"uber/shared/pkg/env"
)

type Config struct {
	JWTSecret    string
	DatabaseURL  string
	RedisAddr    string
	KafkaBrokers []string
	Port         string
	EmailUser    string
	BrevoAPIKey  string
}

func Load() Config {
	return Config{
		JWTSecret:    env.Get("JWT_SECRET", ""),
		DatabaseURL:  env.Get("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/users_db?sslmode=disable"),
		RedisAddr:    env.Get("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers: strings.Split(env.Get("KAFKA_BROKERS", "localhost:9092"), ","),
		Port:         env.Get("PORT", "8081"),
		EmailUser:    env.Get("EMAIL_USER", ""),
		BrevoAPIKey:  env.Get("BREVO_API_KEY", ""),
	}
}
