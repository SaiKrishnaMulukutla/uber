package config

import (
	"strings"

	"uber/shared/pkg/env"
)

type Config struct {
	RedisAddr    string
	KafkaBrokers []string
	Port         string
}

func Load() Config {
	return Config{
		RedisAddr:    env.Get("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers: strings.Split(env.Get("KAFKA_BROKERS", "localhost:9092"), ","),
		Port:         env.Get("PORT", "8087"),
	}
}
