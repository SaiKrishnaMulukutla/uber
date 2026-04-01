package env

import (
	"os"
	"strconv"
)

// Get returns the value of the environment variable key, or fallback if unset/empty.
func Get(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetInt returns the integer value of the environment variable key, or fallback if unset/invalid.
func GetInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
