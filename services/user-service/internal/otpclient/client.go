package otpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

var (
	ErrRateLimited = errors.New("rate limit exceeded — please wait before requesting another OTP")
	ErrInvalidOTP  = errors.New("invalid or expired OTP")
	ErrMaxAttempts = errors.New("max verification attempts exceeded — please request a new OTP")
	ErrUnavailable = errors.New("otp service unavailable")
)

// circuitBreaker opens after 5 consecutive network failures and recovers after 30s.
type circuitBreaker struct {
	mu        sync.Mutex
	failures  int
	threshold int
	openUntil time.Time
}

func newCircuitBreaker() *circuitBreaker {
	return &circuitBreaker{threshold: 5}
}

func (cb *circuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return time.Now().After(cb.openUntil)
}

func (cb *circuitBreaker) success() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.openUntil = time.Time{}
}

func (cb *circuitBreaker) failure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	if cb.failures >= cb.threshold {
		cb.openUntil = time.Now().Add(30 * time.Second)
		cb.failures = 0
	}
}

// Client calls otp-service over HTTP with a circuit breaker.
type Client struct {
	baseURL string
	http    *http.Client
	cb      *circuitBreaker
}

// New returns a Client targeting the given base URL (e.g. "http://otp-service:8086").
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 5 * time.Second},
		cb:      newCircuitBreaker(),
	}
}

// SendOTP calls POST /send-otp.
// otp-service responds after Redis write (fast) — SMTP is async on its side.
func (c *Client) SendOTP(ctx context.Context, email string) error {
	if !c.cb.allow() {
		return ErrUnavailable
	}
	body, _ := json.Marshal(map[string]string{"email": email})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/send-otp", bytes.NewReader(body))
	if err != nil {
		c.cb.failure()
		return ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		c.cb.failure()
		return ErrUnavailable
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		c.cb.success()
		return nil
	case http.StatusTooManyRequests:
		c.cb.success()
		return ErrRateLimited
	default:
		c.cb.failure()
		return ErrUnavailable
	}
}

// VerifyOTP calls POST /verify-otp. Synchronous — caller is waiting for the result.
func (c *Client) VerifyOTP(ctx context.Context, email, otp string) error {
	if !c.cb.allow() {
		return ErrUnavailable
	}
	body, _ := json.Marshal(map[string]string{"email": email, "otp": otp})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/verify-otp", bytes.NewReader(body))
	if err != nil {
		c.cb.failure()
		return ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		c.cb.failure()
		return ErrUnavailable
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		c.cb.success()
		return nil
	case http.StatusBadRequest:
		c.cb.success()
		return ErrInvalidOTP
	case http.StatusTooManyRequests:
		c.cb.success()
		return ErrMaxAttempts
	default:
		c.cb.failure()
		return ErrUnavailable
	}
}
