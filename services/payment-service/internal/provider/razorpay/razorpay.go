package razorpay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	rzp "github.com/razorpay/razorpay-go"

	"uber/payment-service/internal/provider"
)

// Provider implements provider.PaymentProvider using Razorpay.
type Provider struct {
	client        *rzp.Client
	keySecret     string
	webhookSecret string
}

// New returns a Razorpay-backed PaymentProvider.
func New(keyID, keySecret, webhookSecret string) *Provider {
	return &Provider{
		client:        rzp.NewClient(keyID, keySecret),
		keySecret:     keySecret,
		webhookSecret: webhookSecret,
	}
}

// CreateOrder creates a Razorpay order. Amount is in INR; converted to paise internally.
func (p *Provider) CreateOrder(_ context.Context, amount float64, currency, receipt string) (*provider.Order, error) {
	data := map[string]interface{}{
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
// Returns nil, nil for events other than payment.captured.
func (p *Provider) ParseWebhook(body []byte, sig string) (*provider.PaymentResult, error) {
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
		return nil, nil // ignore non-capture events
	}
	return &provider.PaymentResult{
		ProviderPaymentID: payload.Payload.Payment.Entity.ID,
		ProviderOrderID:   payload.Payload.Payment.Entity.OrderID,
	}, nil
}
