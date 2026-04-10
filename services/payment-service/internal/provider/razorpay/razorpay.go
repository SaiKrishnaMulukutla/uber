package razorpay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	rzp "github.com/razorpay/razorpay-go"

	"uber/payment-service/internal/provider"
)

// Provider implements provider.PaymentProvider using Razorpay.
type Provider struct {
	client        *rzp.Client
	keyID         string
	keySecret     string
	webhookSecret string
	httpClient    *http.Client
}

// New returns a Razorpay-backed PaymentProvider.
// skipTLS disables certificate verification — set true only for local dev behind a TLS-inspection proxy.
func New(keyID, keySecret, webhookSecret string, skipTLS bool) *Provider {
	transport := http.DefaultTransport
	if skipTLS {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // local dev only
		}
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	c := rzp.NewClient(keyID, keySecret)
	if skipTLS {
		c.Request.HTTPClient = httpClient
	}
	return &Provider{
		client:        c,
		keyID:         keyID,
		keySecret:     keySecret,
		webhookSecret: webhookSecret,
		httpClient:    httpClient,
	}
}

// CreateOrder creates a Razorpay order. Amount is in INR; converted to paise internally.
func (p *Provider) CreateOrder(_ context.Context, amount float64, currency, receipt string) (*provider.Order, error) {
	data := map[string]any{
		"amount":   int(amount * 100), // paise
		"currency": currency,
		"receipt":  receipt,
	}
	resp, err := p.client.Order.Create(data, nil)
	if err != nil {
		return nil, fmt.Errorf("razorpay create order: %w", err)
	}
	orderID, _ := resp["id"].(string)
	if orderID == "" {
		return nil, errors.New("razorpay: empty order ID in response")
	}
	return &provider.Order{
		ProviderOrderID: orderID,
		Amount:          amount,
		Currency:        currency,
		Receipt:         receipt,
	}, nil
}

// VerifyPayment verifies the HMAC-SHA256 signature returned by the Razorpay checkout SDK.
func (p *Provider) VerifyPayment(_ context.Context, orderID, paymentID, signature string) error {
	payload := orderID + "|" + paymentID
	mac := hmac.New(sha256.New, []byte(p.keySecret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return errors.New("razorpay: payment signature mismatch")
	}
	return nil
}

// ParseWebhook verifies and parses an inbound Razorpay webhook.
// Handles payment.captured (card / UPI). Returns nil, nil for other events.
func (p *Provider) ParseWebhook(body []byte, sig string) (*provider.PaymentResult, error) {
	// Verify HMAC
	mac := hmac.New(sha256.New, []byte(p.webhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return nil, errors.New("razorpay: webhook signature mismatch")
	}

	var payload struct {
		Event   string `json:"event"`
		Payload struct {
			Payment struct {
				Entity struct {
					ID      string `json:"id"`
					OrderID string `json:"order_id"`
				} `json:"entity"`
			} `json:"payment"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("razorpay: parse webhook body: %w", err)
	}

	if payload.Event != "payment.captured" {
		return nil, nil // ignore other events
	}
	return &provider.PaymentResult{
		ProviderPaymentID: payload.Payload.Payment.Entity.ID,
		ProviderOrderID:   payload.Payload.Payment.Entity.OrderID,
	}, nil
}

// razorpayRequest makes a raw HTTP call to the Razorpay API with Basic auth.
func (p *Provider) razorpayRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "https://api.razorpay.com"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(p.keyID, p.keySecret)
	req.Header.Set("Content-Type", "application/json")
	return p.httpClient.Do(req)
}
