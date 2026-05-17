package otp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"uber/shared/pkg/mailer"
)

var (
	ErrRateLimitExceeded   = errors.New("rate limit exceeded: max 3 OTPs per 10 minutes")
	ErrOTPExpired          = errors.New("OTP expired or not found — please request a new one")
	ErrMaxAttemptsExceeded = errors.New("max verification attempts exceeded — please request a new OTP")
	ErrInvalidOTP          = errors.New("invalid OTP")
)

const (
	otpTTL           = 300 * time.Second
	attemptsTTL      = 300 * time.Second
	rateTTL          = 600 * time.Second
	maxSendPerWindow = 3
	maxVerifyAttempts = 5
)

var luaIncrRate = goredis.NewScript(`
local count = redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], ARGV[1])
return count
`)

var luaIncrAttempts = goredis.NewScript(`
local count = redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], ARGV[1])
return count
`)

// Client handles OTP generation, storage, and verification directly via Redis.
type Client struct {
	rdb    goredis.UniversalClient
	mailer mailer.Mailer
}

// New returns an OTP Client backed by the given Redis client and mailer.
func New(rdb goredis.UniversalClient, m mailer.Mailer) *Client {
	return &Client{rdb: rdb, mailer: m}
}

// SendOTP generates a 6-digit OTP, stores its SHA256 hash in Redis, and emails it.
func (c *Client) SendOTP(ctx context.Context, email string) error {
	count, err := luaIncrRate.Run(ctx, c.rdb, []string{"rate:" + email}, int(rateTTL.Seconds())).Int64()
	if err != nil {
		return fmt.Errorf("otp: rate check: %w", err)
	}
	if count > maxSendPerWindow {
		return ErrRateLimitExceeded
	}

	otp, err := generateOTP()
	if err != nil {
		return fmt.Errorf("otp: generate: %w", err)
	}

	if err := c.rdb.Set(ctx, "otp:"+email, hashOTP(otp), otpTTL).Err(); err != nil {
		return fmt.Errorf("otp: store: %w", err)
	}
	c.rdb.Del(ctx, "attempts:"+email)

	if c.mailer != nil {
		if err := c.mailer.Send(email, "Your RideGo Login Code", mailer.LoginOTP(otp)); err != nil {
			fmt.Printf("[otp] warn: email send failed for %s: %v\n", email, err)
		}
	}
	return nil
}

// VerifyOTP checks the provided OTP against the stored hash.
// Deletes the OTP on success to prevent reuse.
func (c *Client) VerifyOTP(ctx context.Context, email, otp string) error {
	storedHash, err := c.rdb.Get(ctx, "otp:"+email).Result()
	if errors.Is(err, goredis.Nil) {
		return ErrOTPExpired
	}
	if err != nil {
		return fmt.Errorf("otp: get: %w", err)
	}

	attempts, err := c.incrAttempts(ctx, email)
	if err != nil {
		return fmt.Errorf("otp: incr attempts: %w", err)
	}
	if attempts > maxVerifyAttempts {
		return ErrMaxAttemptsExceeded
	}

	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hashOTP(otp))) != 1 {
		return ErrInvalidOTP
	}

	c.rdb.Del(ctx, "otp:"+email)
	c.rdb.Del(ctx, "attempts:"+email)
	return nil
}

func (c *Client) incrAttempts(ctx context.Context, email string) (int64, error) {
	count, err := luaIncrAttempts.Run(ctx, c.rdb, []string{"attempts:" + email}, int(attemptsTTL.Seconds())).Int64()
	if err != nil {
		return 0, err
	}
	return count, nil
}

func generateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func hashOTP(otp string) string {
	sum := sha256.Sum256([]byte(otp))
	return hex.EncodeToString(sum[:])
}

