package cash

import (
	"context"
	"errors"

	"uber/payment-service/internal/provider"
)

// Provider is a no-op PaymentProvider for cash payments.
// Cash payments are completed immediately without provider interaction.
type Provider struct{}

// New returns a cash Provider.
func New() *Provider { return &Provider{} }

// CreateOrder is unreachable for cash — InitPayment short-circuits before calling this.
func (p *Provider) CreateOrder(_ context.Context, _ float64, _, _ string) (*provider.Order, error) {
	return nil, errors.New("cash payments do not use provider orders")
}

// VerifyPayment is a no-op for cash.
func (p *Provider) VerifyPayment(_ context.Context, _, _, _ string) error { return nil }

// ParseWebhook is a no-op for cash.
func (p *Provider) ParseWebhook(_ []byte, _ string) (*provider.PaymentResult, error) {
	return nil, nil
}
