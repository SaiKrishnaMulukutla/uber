package payments

import "time"

const (
	StatusPending   = "PENDING"
	StatusCompleted = "COMPLETED"
	StatusFailed    = "FAILED"
)

// Payment represents a payment record.
type Payment struct {
	ID            string     `json:"id"`
	TripID        string     `json:"trip_id"`
	RiderID       string     `json:"rider_id"`
	DriverID      string     `json:"driver_id"`
	Amount        float64    `json:"amount"`
	Status        string     `json:"status"`
	PaymentMethod string     `json:"payment_method"`
	CreatedAt     time.Time  `json:"created_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

// PaymentHistoryResponse is the paginated response for GET /payments/history.
type PaymentHistoryResponse struct {
	Payments []*Payment `json:"payments"`
	Total    int        `json:"total"`
	Limit    int        `json:"limit"`
	Offset   int        `json:"offset"`
}
