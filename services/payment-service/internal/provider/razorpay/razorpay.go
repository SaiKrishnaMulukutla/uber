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
	"io"
	"log"
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

// CreateOrder creates a Razorpay order for card payments. Amount is in INR; converted to paise internally.
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

// CreateUPIOrder creates a Razorpay order (for VPA collect) + a fixed-amount QR code (for QR scan).
func (p *Provider) CreateUPIOrder(ctx context.Context, amount float64, currency, receipt string) (*provider.UPIOrder, error) {
	// 1. Create order for VPA collect path
	order, err := p.CreateOrder(ctx, amount, currency, receipt)
	if err != nil {
		return nil, err
	}

	// 2. Create QR code for scan path (best-effort — failure is non-fatal)
	qrID, qrURL, err := p.createQRCode(ctx, amount, receipt)
	if err != nil {
		log.Printf("[razorpay] QR code creation failed (non-fatal): %v", err)
	}

	return &provider.UPIOrder{
		ProviderOrderID: order.ProviderOrderID,
		QRCodeID:        qrID,
		QRImageURL:      qrURL,
		Amount:          amount,
		Currency:        currency,
	}, nil
}

// createQRCode calls the Razorpay QR Codes API and returns (qr_id, image_url, error).
func (p *Provider) createQRCode(ctx context.Context, amount float64, receipt string) (string, string, error) {
	body, _ := json.Marshal(map[string]any{
		"type":                  "upi_qr",
		"name":                  "Uber Trip " + receipt,
		"usage":                 "single_use",
		"fixed_amount":          true,
		"payment_amount":        int(amount * 100), // paise
		"close_by":              time.Now().Add(time.Hour).Unix(),
		"close_on_full_payment": true,
		"description":           "Trip payment",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.razorpay.com/v1/payments/qr-codes", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.SetBasicAuth(p.keyID, p.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID       string `json:"id"`
		ImageURL string `json:"image_url"`
		Error    struct {
			Description string `json:"description"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode qr response: %w", err)
	}
	if result.ID == "" {
		return "", "", fmt.Errorf("razorpay QR: %s", result.Error.Description)
	}
	return result.ID, result.ImageURL, nil
}

// InitiateUPICollect sends a UPI collect request to the rider's VPA via Razorpay.
func (p *Provider) InitiateUPICollect(ctx context.Context, orderID, vpa, phone, email string) error {
	if phone == "" {
		phone = "+919999999999" // fallback for test mode
	}
	body, _ := json.Marshal(map[string]any{
		"type":     "link",
		"method":   "upi",
		"vpa":      vpa,
		"order_id": orderID,
		"amount":   nil, // amount comes from the order
		"currency": "INR",
		"email":    email,
		"contact":  phone,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.razorpay.com/v1/payments/create/upi", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(p.keyID, p.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upi collect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upi collect error (%d): %s", resp.StatusCode, string(raw))
	}
	return nil
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
// Handles payment.captured (card / UPI VPA) and qr_code.credited (UPI QR).
// Returns nil, nil for events that should be ignored.
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
			QRCode struct {
				Entity struct {
					ID string `json:"id"`
				} `json:"entity"`
			} `json:"qr_code"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("razorpay: parse webhook body: %w", err)
	}

	switch payload.Event {
	case "payment.captured":
		return &provider.PaymentResult{
			ProviderPaymentID: payload.Payload.Payment.Entity.ID,
			ProviderOrderID:   payload.Payload.Payment.Entity.OrderID,
		}, nil
	case "qr_code.credited":
		return &provider.PaymentResult{
			ProviderPaymentID: payload.Payload.Payment.Entity.ID,
			ProviderQRID:      payload.Payload.QRCode.Entity.ID,
		}, nil
	default:
		return nil, nil // ignore other events
	}
}

// RegisterWebhook registers a webhook URL with Razorpay subscribed to payment events.
func (p *Provider) RegisterWebhook(ctx context.Context, webhookURL string) error {
	body, _ := json.Marshal(map[string]any{
		"url":    webhookURL,
		"secret": p.webhookSecret,
		"active": true,
		"subscriptions": map[string]any{
			"active": []string{"payment.captured", "qr_code.credited"},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.razorpay.com/v1/webhooks", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(p.keyID, p.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("register webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register webhook error (%d): %s", resp.StatusCode, string(raw))
	}
	log.Printf("[razorpay] webhook registered at %s", webhookURL)
	return nil
}
