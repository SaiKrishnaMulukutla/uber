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
		if err := c.mailer.Send(email, "Your Uber Login Code", buildOTPEmail(otp)); err != nil {
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
	key := "attempts:" + email
	count, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	c.rdb.Expire(ctx, key, attemptsTTL)
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

func buildOTPEmail(otp string) string {
	content := fmt.Sprintf(`
<p style="margin:0 0 8px;font-size:22px;font-weight:700;color:#000000;">Your login verification code</p>
<p style="margin:0 0 28px;font-size:15px;color:#545454;line-height:1.6;">
  Use the code below to complete your sign-in. It expires in <strong>5 minutes</strong> and can only be used once.
</p>
<table cellpadding="0" cellspacing="0" border="0" style="margin-bottom:28px;">
  <tr><td style="background-color:#f6f6f6;border-radius:4px;padding:20px 36px;text-align:center;">
    <span style="font-size:40px;font-weight:700;letter-spacing:14px;color:#000000;">%s</span>
  </td></tr>
</table>
<p style="margin:0;font-size:13px;color:#888888;">
  If you didn't request this code, you can safely ignore this email.
</p>`, otp)

	return fmt.Sprintf(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"/></head>
<body style="margin:0;padding:0;background-color:#f6f6f6;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="padding:40px 0;">
    <tr><td align="center">
      <table width="520" cellpadding="0" cellspacing="0" border="0"
             style="background:#ffffff;border-radius:4px;box-shadow:0 2px 8px rgba(0,0,0,0.08);">
        <tr><td style="background:#000000;padding:28px 40px;">
          <span style="color:#ffffff;font-size:26px;font-weight:700;">Uber</span>
        </td></tr>
        <tr><td style="padding:40px 40px 28px;">%s</td></tr>
        <tr><td style="padding:24px 40px 32px;text-align:center;">
          <p style="margin:0;font-size:12px;color:#aaaaaa;">This is an automated message. Please do not reply.</p>
          <p style="margin:8px 0 0;font-size:12px;color:#555555;">Created with &#10084;&#65039; by Mulukutla Sai Krishna</p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`, content)
}
