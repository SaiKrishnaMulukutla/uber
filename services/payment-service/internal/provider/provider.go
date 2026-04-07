package provider

import "context"

// Order represents a payment order created with a provider.
type Order struct {
	ProviderOrderID string
	Amount          float64
	Currency        string
	Receipt         string
}

// PaymentResult is returned when a webhook confirms a payment.
type PaymentResult struct {
	ProviderPaymentID string
	ProviderOrderID   string
}

// PaymentProvider is a generic interface for payment gateways.
type PaymentProvider interface {
	// CreateOrder creates a new payment order with the provider.
	CreateOrder(ctx context.Context, amount float64, currency, receipt string) (*Order, error)
	// VerifyPayment checks the signature returned by the frontend after checkout.
	VerifyPayment(ctx context.Context, orderID, paymentID, signature string) error
	// ParseWebhook verifies and parses an inbound webhook payload.
	// Returns nil, nil for events that should be ignored.
	ParseWebhook(body []byte, webhookSignature string) (*PaymentResult, error)
}
