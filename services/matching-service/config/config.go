package config

import (
	"strings"

	"uber/shared/pkg/env"
)

type Config struct {
	RedisAddr    string
	KafkaBrokers []string
}

func Load() Config {
	return Config{
		RedisAddr:    env.Get("REDIS_ADDR", "localhost:6379"),
		KafkaBrokers: strings.Split(env.Get("KAFKA_BROKERS", "localhost:9092"), ","),
	}
}
