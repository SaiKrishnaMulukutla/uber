package cash

import (
	"context"
	"errors"

	"uber/payment-service/internal/provider"
)

// Provider is a no-op PaymentProvider for cash payments.
type Provider struct{}

// New returns a cash Provider.
func New() *Provider { return &Provider{} }

// CreateOrder is unreachable for cash — service short-circuits before calling this.
func (p *Provider) CreateOrder(_ context.Context, _ float64, _, _ string) (*provider.Order, error) {
	return nil, errors.New("cash payments do not use provider orders")
}

// CreateUPIOrder is unreachable for cash.
func (p *Provider) CreateUPIOrder(_ context.Context, _ float64, _, _ string) (*provider.UPIOrder, error) {
	return nil, errors.New("cash payments do not use UPI orders")
}

// InitiateUPICollect is unreachable for cash.
func (p *Provider) InitiateUPICollect(_ context.Context, _, _, _, _ string) error {
	return errors.New("cash payments do not use UPI collect")
}

// VerifyPayment is a no-op for cash.
func (p *Provider) VerifyPayment(_ context.Context, _, _, _ string) error { return nil }

// ParseWebhook is a no-op for cash.
func (p *Provider) ParseWebhook(_ []byte, _ string) (*provider.PaymentResult, error) {
	return nil, nil
}

// RegisterWebhook is a no-op for cash.
func (p *Provider) RegisterWebhook(_ context.Context, _ string) error { return nil }
