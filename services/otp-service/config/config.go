package config

import "uber/shared/pkg/env"

// Config holds all environment-driven configuration for the OTP service.
type Config struct {
	Port      string
	RedisAddr string
	EmailHost string
	EmailPort int
	EmailUser string
	EmailPass string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() Config {
	return Config{
		Port:      env.Get("PORT", "8080"),
		RedisAddr: env.Get("REDIS_ADDR", "localhost:6379"),
		EmailHost: env.Get("EMAIL_HOST", "smtp.gmail.com"),
		EmailPort: env.GetInt("EMAIL_PORT", 587),
		EmailUser: env.Get("EMAIL_USER", ""),
		EmailPass: env.Get("EMAIL_PASS", ""),
	}
}
