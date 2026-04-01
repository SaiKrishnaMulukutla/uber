package repository

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	otpTTL      = 300 * time.Second // 5 minutes
	attemptsTTL = 300 * time.Second // matches OTP lifetime
	rateTTL     = 600 * time.Second // 10-minute sliding window
)

// ErrNotFound is returned when the requested key does not exist in Redis.
var ErrNotFound = errors.New("key not found")

// Repository defines all Redis operations needed by the OTP service.
type Repository interface {
	// OTP storage
	StoreOTP(ctx context.Context, email, hashedOTP string) error
	GetOTP(ctx context.Context, email string) (string, error)
	DeleteOTP(ctx context.Context, email string) error

	// Attempt tracking (per OTP lifetime)
	IncrAttempts(ctx context.Context, email string) (int64, error)
	ResetAttempts(ctx context.Context, email string) error

	// Rate limiting (per 10-min window)
	GetSendCount(ctx context.Context, email string) (int64, error)
	IncrRateLimit(ctx context.Context, email string) error
}

type redisRepo struct{ client *redis.Client }

// New returns a Redis-backed Repository.
func New(client *redis.Client) Repository {
	return &redisRepo{client: client}
}

func otpKey(email string) string      { return "otp:" + email }
func attemptsKey(email string) string { return "attempts:" + email }
func rateKey(email string) string     { return "rate:" + email }

// StoreOTP persists the hashed OTP with a 5-minute TTL.
// Overwrites any existing OTP for the same email.
func (r *redisRepo) StoreOTP(ctx context.Context, email, hashedOTP string) error {
	return r.client.Set(ctx, otpKey(email), hashedOTP, otpTTL).Err()
}

// GetOTP retrieves the stored hashed OTP. Returns ErrNotFound if expired or absent.
func (r *redisRepo) GetOTP(ctx context.Context, email string) (string, error) {
	val, err := r.client.Get(ctx, otpKey(email)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrNotFound
	}
	return val, err
}

// DeleteOTP removes the OTP key (called after successful verification).
func (r *redisRepo) DeleteOTP(ctx context.Context, email string) error {
	return r.client.Del(ctx, otpKey(email)).Err()
}

// IncrAttempts atomically increments the attempt counter.
// Sets a TTL on first increment so the key expires with the OTP.
func (r *redisRepo) IncrAttempts(ctx context.Context, email string) (int64, error) {
	key := attemptsKey(email)
	var count int64
	_, err := r.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		incr := pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, attemptsTTL)
		count = incr.Val()
		return nil
	})
	if err != nil {
		// Fall back to non-pipelined INCR
		count, err = r.client.Incr(ctx, key).Result()
		if err != nil {
			return 0, err
		}
		r.client.Expire(ctx, key, attemptsTTL)
	}
	return count, nil
}

// ResetAttempts removes the attempt counter (called on new OTP send or success).
func (r *redisRepo) ResetAttempts(ctx context.Context, email string) error {
	return r.client.Del(ctx, attemptsKey(email)).Err()
}

// GetSendCount returns how many OTPs have been sent in the current 10-min window.
// Returns 0 if no key exists.
func (r *redisRepo) GetSendCount(ctx context.Context, email string) (int64, error) {
	val, err := r.client.Get(ctx, rateKey(email)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return val, err
}

// IncrRateLimit atomically increments the rate-limit counter.
// Sets TTL only on first increment (preserves the sliding window expiry).
func (r *redisRepo) IncrRateLimit(ctx context.Context, email string) error {
	key := rateKey(email)
	_, err := r.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		incr := pipe.Incr(ctx, key)
		// Only set TTL when the key is newly created (count == 1)
		pipe.ExpireNX(ctx, key, rateTTL)
		_ = incr
		return nil
	})
	return err
}
