package events

// LatLng is a coordinate pair used in event payloads.
type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// RideRequestedEvent is published to ride.requested.
type RideRequestedEvent struct {
	TripID      string `json:"trip_id"`
	RiderID     string `json:"rider_id"`
	Pickup      LatLng `json:"pickup"`
	Drop        LatLng `json:"drop"`
	RequestedAt string `json:"requested_at"`
}

// DriverAssignedEvent is published to driver.assigned.
type DriverAssignedEvent struct {
	TripID   string `json:"trip_id"`
	DriverID string `json:"driver_id"`
}

// TripCompletedEvent is published to trip.completed.
type TripCompletedEvent struct {
	TripID          string  `json:"trip_id"`
	DriverID        string  `json:"driver_id"`
	RiderID         string  `json:"rider_id"`
	Fare            float64 `json:"fare"`
	CompletedAt     string  `json:"completed_at"`
	DurationSeconds int64   `json:"duration_seconds"`
}

// TripCancelledEvent is published to trip.cancelled.
type TripCancelledEvent struct {
	TripID      string `json:"trip_id"`
	DriverID    string `json:"driver_id,omitempty"`
	RiderID     string `json:"rider_id"`
	Reason      string `json:"reason,omitempty"`
	CancelledAt string `json:"cancelled_at"`
}

// RatingSubmittedEvent is published to rating.submitted.
type RatingSubmittedEvent struct {
	TripID    string `json:"trip_id"`
	RaterID   string `json:"rater_id"`
	RaterRole string `json:"rater_role"`
	RateeID   string `json:"ratee_id"`
	RateeRole string `json:"ratee_role"`
	Score     int    `json:"score"`
	Comment   string `json:"comment,omitempty"`
}

// PaymentCompletedEvent is published to payment.completed.
type PaymentCompletedEvent struct {
	PaymentID   string  `json:"payment_id"`
	TripID      string  `json:"trip_id"`
	RiderID     string  `json:"rider_id"`
	DriverID    string  `json:"driver_id"`
	Amount      float64 `json:"amount"`
	CompletedAt string  `json:"completed_at"`
}
