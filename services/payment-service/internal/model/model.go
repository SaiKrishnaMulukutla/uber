package model

import "time"

const (
	StatusPending             = "PENDING"
	StatusProcessing          = "PROCESSING"
	StatusAwaitingCashConfirm = "AWAITING_CASH_CONFIRM"
	StatusCompleted           = "COMPLETED"
	StatusFailed              = "FAILED"
)

type Payment struct {
	ID                string     `json:"id"`
	TripID            string     `json:"trip_id"`
	RiderID           string     `json:"rider_id"`
	RiderEmail        string     `json:"rider_email,omitempty"`
	DriverID          string     `json:"driver_id"`
	Amount            float64    `json:"amount"`
	Status            string     `json:"status"`
	PaymentMethod     string     `json:"payment_method"`
	Provider          string     `json:"provider"`
	ProviderOrderID   string     `json:"provider_order_id,omitempty"`
	ProviderPaymentID string     `json:"provider_payment_id,omitempty"`
	FailureReason     string     `json:"failure_reason,omitempty"`
	AttemptsCount     int        `json:"attempts_count"`
	CreatedAt         time.Time  `json:"created_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// OrderResponse is returned to the frontend after CreateOrder.
type OrderResponse struct {
	PaymentID       string  `json:"payment_id"`
	ProviderOrderID string  `json:"provider_order_id"`
	Amount          float64 `json:"amount"`
	Currency        string  `json:"currency"`
	KeyID           string  `json:"key_id"`
	CheckoutURL     string  `json:"checkout_url"`
}

// VerifyRequest is the frontend POST body for /payments/verify.
type VerifyRequest struct {
	PaymentID         string `json:"payment_id"`
	ProviderOrderID   string `json:"provider_order_id"`
	ProviderPaymentID string `json:"provider_payment_id"`
	Signature         string `json:"signature"`
}

type PaymentHistoryResponse struct {
	Payments []*Payment `json:"payments"`
	Total    int        `json:"total"`
	Limit    int        `json:"limit"`
	Offset   int        `json:"offset"`
}
