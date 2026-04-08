package config

import (
	"strings"

	"uber/shared/pkg/env"
)

type Config struct {
	JWTSecret    string
	DatabaseURL  string
	KafkaBrokers []string
	Port         string
	EmailHost    string
	EmailPort    int
	EmailUser    string
	EmailPass    string
}

func Load() Config {
	return Config{
		JWTSecret:    env.Get("JWT_SECRET", ""),
		DatabaseURL:  env.Get("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/notifications_db?sslmode=disable"),
		KafkaBrokers: strings.Split(env.Get("KAFKA_BROKERS", "localhost:9092"), ","),
		Port:         env.Get("PORT", "8084"),
		EmailHost:    env.Get("EMAIL_HOST", "smtp.gmail.com"),
		EmailPort:    env.GetInt("EMAIL_PORT", 587),
		EmailUser:    env.Get("EMAIL_USER", ""),
		EmailPass:    env.Get("EMAIL_PASS", ""),
	}
}
