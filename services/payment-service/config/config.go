package config

import (
	"strings"

	"uber/shared/pkg/env"
)

type Config struct {
	JWTSecret             string
	DatabaseURL           string
	KafkaBrokers          []string
	Port                  string
	PaymentProvider       string // "razorpay" | "cash"
	RazorpayKeyID         string
	RazorpayKeySecret     string
	RazorpayWebhookSecret string
}

func Load() Config {
	return Config{
		JWTSecret:             env.Get("JWT_SECRET", ""),
		DatabaseURL:           env.Get("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/payments_db?sslmode=disable"),
		KafkaBrokers:          strings.Split(env.Get("KAFKA_BROKERS", "localhost:9092"), ","),
		Port:                  env.Get("PORT", "8085"),
		PaymentProvider:       env.Get("PAYMENT_PROVIDER", "cash"),
		RazorpayKeyID:         env.Get("RAZORPAY_KEY_ID", ""),
		RazorpayKeySecret:     env.Get("RAZORPAY_KEY_SECRET", ""),
		RazorpayWebhookSecret: env.Get("RAZORPAY_WEBHOOK_SECRET", ""),
	}
}
