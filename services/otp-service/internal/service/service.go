package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"uber/otp-service/internal/mailer"
	"uber/otp-service/internal/repository"
)

// Sentinel errors returned by the service layer.
var (
	ErrRateLimitExceeded   = errors.New("rate limit exceeded: max 3 OTPs per 10 minutes")
	ErrOTPExpired          = errors.New("OTP expired or not found — please request a new one")
	ErrMaxAttemptsExceeded = errors.New("max verification attempts exceeded — please request a new OTP")
	ErrInvalidOTP          = errors.New("invalid OTP")
)

// OTPService defines the business operations for OTP auth.
type OTPService interface {
	SendOTP(ctx context.Context, email string) error
	VerifyOTP(ctx context.Context, email, otp string) error
}

type otpService struct {
	repo   repository.Repository
	mailer mailer.Mailer
}

const (
	maxSendPerWindow   = 3
	maxVerifyAttempts  = 5
)

// New returns an OTPService backed by the given repository and mailer.
func New(repo repository.Repository, m mailer.Mailer) OTPService {
	return &otpService{repo: repo, mailer: m}
}

// SendOTP generates a 6-digit OTP, stores its SHA256 hash, and emails it.
func (s *otpService) SendOTP(ctx context.Context, email string) error {
	// 1. Rate limit check
	count, err := s.repo.GetSendCount(ctx, email)
	if err != nil {
		return fmt.Errorf("service: rate check failed: %w", err)
	}
	if count >= maxSendPerWindow {
		return ErrRateLimitExceeded
	}

	// 2. Generate cryptographically secure 6-digit OTP
	otp, err := generateOTP()
	if err != nil {
		return fmt.Errorf("service: otp generation failed: %w", err)
	}

	// 3. Hash OTP with SHA256
	hashed := hashOTP(otp)

	// 4. Persist hash (overwrites any existing OTP, resets its TTL)
	if err := s.repo.StoreOTP(ctx, email, hashed); err != nil {
		return fmt.Errorf("service: store otp failed: %w", err)
	}

	// 5. Reset attempt counter so this OTP gets a fresh 5 tries
	if err := s.repo.ResetAttempts(ctx, email); err != nil {
		return fmt.Errorf("service: reset attempts failed: %w", err)
	}

	// 6. Increment rate-limit counter (TTL set only on first increment)
	if err := s.repo.IncrRateLimit(ctx, email); err != nil {
		return fmt.Errorf("service: rate limit incr failed: %w", err)
	}

	// 7. Send OTP via email
	subject := "Your Uber Login Code"
	body := buildOTPEmailBody(otp)
	if err := s.mailer.Send(email, subject, body); err != nil {
		return fmt.Errorf("service: email send failed: %w", err)
	}

	return nil
}

// VerifyOTP checks the provided OTP against the stored hash.
// Returns nil on success. Deletes the OTP from Redis to prevent reuse.
func (s *otpService) VerifyOTP(ctx context.Context, email, otp string) error {
	// 1. Retrieve stored hash — absence means expired or never sent
	storedHash, err := s.repo.GetOTP(ctx, email)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrOTPExpired
	}
	if err != nil {
		return fmt.Errorf("service: get otp failed: %w", err)
	}

	// 2. Increment attempt counter BEFORE checking — prevents timing-based bypass
	attempts, err := s.repo.IncrAttempts(ctx, email)
	if err != nil {
		return fmt.Errorf("service: incr attempts failed: %w", err)
	}
	if attempts > maxVerifyAttempts {
		return ErrMaxAttemptsExceeded
	}

	// 3. Hash the input and compare using constant-time comparison
	inputHash := hashOTP(otp)
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(inputHash)) != 1 {
		return ErrInvalidOTP
	}

	// 4. Verified — delete OTP and reset attempts to prevent reuse
	_ = s.repo.DeleteOTP(ctx, email)
	_ = s.repo.ResetAttempts(ctx, email)

	return nil
}

func buildOTPEmailBody(otp string) string {
	content := fmt.Sprintf(`
<p style="margin:0 0 8px;font-size:22px;font-weight:700;color:#000000;">Your login verification code</p>
<p style="margin:0 0 28px;font-size:15px;color:#545454;line-height:1.6;">
  Use the code below to complete your sign-in. For your security,
  this code expires in <strong>5 minutes</strong> and can only be used once.
</p>
<table cellpadding="0" cellspacing="0" border="0" style="margin-bottom:28px;">
  <tr><td style="background-color:#f6f6f6;border-radius:4px;padding:20px 36px;text-align:center;">
    <span style="font-size:40px;font-weight:700;letter-spacing:14px;color:#000000;font-variant-numeric:tabular-nums;">%s</span>
  </td></tr>
</table>
<p style="margin:0;font-size:13px;color:#888888;line-height:1.6;">
  If you didn't request this code, you can safely ignore this email.
  Someone else might have typed your email address by mistake.
</p>`, otp)
	return buildEmailLayout(content)
}

func buildEmailLayout(content string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"/><title>Uber</title></head>
<body style="margin:0;padding:0;background-color:#f6f6f6;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f6f6f6;padding:40px 0;">
    <tr><td align="center">
      <table width="520" cellpadding="0" cellspacing="0" border="0"
             style="background-color:#ffffff;border-radius:4px;overflow:hidden;box-shadow:0 2px 8px rgba(0,0,0,0.08);">
        <tr><td style="background-color:#000000;padding:28px 40px;">
          <span style="color:#ffffff;font-size:26px;font-weight:700;letter-spacing:-0.5px;">Uber</span>
        </td></tr>
        <tr><td style="padding:40px 40px 28px;">%s</td></tr>
        <tr><td style="padding:0 40px;"><hr style="border:none;border-top:1px solid #eeeeee;margin:0;"/></td></tr>
        <tr><td style="padding:24px 40px 32px;">
          <p style="margin:0 0 4px;font-size:12px;color:#aaaaaa;line-height:1.6;">
            This is an automated message from Uber. Please do not reply to this email.
          </p>
          <p style="margin:8px 0 0;font-size:12px;color:#555555;line-height:1.6;text-align:center;">
            Created with &#10084;&#65039; by Mulukutla Sai Krishna
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, content)
}

// generateOTP returns a zero-padded 6-digit string using crypto/rand.
func generateOTP() (string, error) {
	max := big.NewInt(1_000_000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// hashOTP returns the lowercase hex SHA256 digest of the given OTP string.
func hashOTP(otp string) string {
	sum := sha256.Sum256([]byte(otp))
	return hex.EncodeToString(sum[:])
}
