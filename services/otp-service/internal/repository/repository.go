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

	// IncrAndCheckRateLimit atomically increments the send counter and returns the
	// new value. The caller rejects the request if the returned count exceeds the max.
	// Using a Lua script makes the read-increment-check a single atomic operation,
	// preventing concurrent requests from bypassing the limit.
	IncrAndCheckRateLimit(ctx context.Context, email string) (int64, error)
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

// luaIncrRate is a Lua script that atomically increments the rate-limit counter
// and resets its TTL. Executing as a single script prevents the TOCTOU race
// where two concurrent requests both read the same count and both pass the check.
var luaIncrRate = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], ARGV[1])
return count
`)

// IncrAndCheckRateLimit atomically increments the sliding-window send counter
// and returns the new value. The caller rejects if count exceeds the maximum.
func (r *redisRepo) IncrAndCheckRateLimit(ctx context.Context, email string) (int64, error) {
	ttlSec := int(rateTTL.Seconds())
	res, err := luaIncrRate.Run(ctx, r.client, []string{rateKey(email)}, ttlSec).Int64()
	if err != nil {
		return 0, err
	}
	return res, nil
}
