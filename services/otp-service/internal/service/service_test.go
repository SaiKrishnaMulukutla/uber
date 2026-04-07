package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"uber/otp-service/internal/repository"
)

// --- mocks ---

type mockRepo struct {
	otp      map[string]string // email → hashed otp
	attempts map[string]int64
	rate     map[string]int64
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		otp:      make(map[string]string),
		attempts: make(map[string]int64),
		rate:     make(map[string]int64),
	}
}

func (m *mockRepo) StoreOTP(_ context.Context, email, hashed string) error {
	m.otp[email] = hashed
	return nil
}
func (m *mockRepo) GetOTP(_ context.Context, email string) (string, error) {
	v, ok := m.otp[email]
	if !ok {
		return "", repository.ErrNotFound
	}
	return v, nil
}
func (m *mockRepo) DeleteOTP(_ context.Context, email string) error {
	delete(m.otp, email)
	return nil
}
func (m *mockRepo) IncrAttempts(_ context.Context, email string) (int64, error) {
	m.attempts[email]++
	return m.attempts[email], nil
}
func (m *mockRepo) ResetAttempts(_ context.Context, email string) error {
	m.attempts[email] = 0
	return nil
}
func (m *mockRepo) IncrAndCheckRateLimit(_ context.Context, email string) (int64, error) {
	m.rate[email]++
	return m.rate[email], nil
}

type mockMailer struct{ err error }

func (m *mockMailer) Send(_, _, _ string) error { return m.err }

// --- helpers ---

func hashStr(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// --- tests ---

func TestSendOTP_Success(t *testing.T) {
	repo := newMockRepo()
	svc := New(repo, &mockMailer{})

	if err := svc.SendOTP(context.Background(), "user@test.com"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if repo.rate["user@test.com"] != 1 {
		t.Errorf("expected rate count 1, got %d", repo.rate["user@test.com"])
	}
}

func TestSendOTP_RateLimit(t *testing.T) {
	repo := newMockRepo()
	repo.rate["user@test.com"] = 3 // already at limit
	svc := New(repo, &mockMailer{})

	err := svc.SendOTP(context.Background(), "user@test.com")
	if !errors.Is(err, ErrRateLimitExceeded) {
		t.Fatalf("expected ErrRateLimitExceeded, got %v", err)
	}
}

func TestVerifyOTP_Success(t *testing.T) {
	repo := newMockRepo()
	const otp = "123456"
	repo.otp["user@test.com"] = hashStr(otp)
	svc := New(repo, &mockMailer{})

	if err := svc.VerifyOTP(context.Background(), "user@test.com", otp); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	// OTP must be deleted after success
	if _, ok := repo.otp["user@test.com"]; ok {
		t.Error("expected OTP to be deleted after successful verification")
	}
}

func TestVerifyOTP_WrongOTP(t *testing.T) {
	repo := newMockRepo()
	repo.otp["user@test.com"] = hashStr("654321")
	svc := New(repo, &mockMailer{})

	err := svc.VerifyOTP(context.Background(), "user@test.com", "000000")
	if !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("expected ErrInvalidOTP, got %v", err)
	}
}

func TestVerifyOTP_Expired(t *testing.T) {
	repo := newMockRepo() // no OTP stored
	svc := New(repo, &mockMailer{})

	err := svc.VerifyOTP(context.Background(), "user@test.com", "123456")
	if !errors.Is(err, ErrOTPExpired) {
		t.Fatalf("expected ErrOTPExpired, got %v", err)
	}
}

func TestVerifyOTP_MaxAttempts(t *testing.T) {
	repo := newMockRepo()
	repo.otp["user@test.com"] = hashStr("999999")
	repo.attempts["user@test.com"] = 5 // already at limit
	svc := New(repo, &mockMailer{})

	err := svc.VerifyOTP(context.Background(), "user@test.com", "000000")
	if !errors.Is(err, ErrMaxAttemptsExceeded) {
		t.Fatalf("expected ErrMaxAttemptsExceeded, got %v", err)
	}
}
