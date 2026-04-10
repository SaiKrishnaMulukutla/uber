package provider

import "context"

// Order represents a payment order created with a provider.
type Order struct {
	ProviderOrderID string
	Amount          float64
	Currency        string
	Receipt         string
}

// UPIOrder is returned by CreateUPIOrder — includes both the Razorpay order (for VPA collect)
// and the QR code details (for QR scan).
type UPIOrder struct {
	ProviderOrderID string
	QRCodeID        string
	QRImageURL      string
	Amount          float64
	Currency        string
}

// PaymentResult is returned when a webhook confirms a payment.
type PaymentResult struct {
	ProviderPaymentID string
	ProviderOrderID   string // set for payment.captured (card / UPI VPA)
	ProviderQRID      string // set for qr_code.credited (UPI QR scan)
}

// PaymentProvider is a generic interface for payment gateways.
type PaymentProvider interface {
	CreateOrder(ctx context.Context, amount float64, currency, receipt string) (*Order, error)
	CreateUPIOrder(ctx context.Context, amount float64, currency, receipt string) (*UPIOrder, error)
	InitiateUPICollect(ctx context.Context, orderID, vpa, phone, email string) error
	VerifyPayment(ctx context.Context, orderID, paymentID, signature string) error
	ParseWebhook(body []byte, webhookSignature string) (*PaymentResult, error)
	RegisterWebhook(ctx context.Context, webhookURL string) error
}
