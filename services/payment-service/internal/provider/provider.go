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
	CreateOrder(ctx context.Context, amount float64, currency, receipt string) (*Order, error)
	VerifyPayment(ctx context.Context, orderID, paymentID, signature string) error
	ParseWebhook(body []byte, webhookSignature string) (*PaymentResult, error)
}
