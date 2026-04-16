package kafka

// LatLng is a coordinate pair used in event payloads.
type LatLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// RideRequestedEvent is published to ride.requested.
type RideRequestedEvent struct {
	TripID        string   `json:"trip_id"`
	RiderID       string   `json:"rider_id"`
	Pickup        LatLng   `json:"pickup"`
	Drop          LatLng   `json:"drop"`
	RequestedAt   string   `json:"requested_at"`
	VehicleType   string   `json:"vehicle_type,omitempty"` // go | x | xl
	RetryCount    int      `json:"retry_count,omitempty"`
	SkipDriverIDs []string `json:"skip_driver_ids,omitempty"`
}

// RideOfferedEvent is published to ride.offered when matching selects a driver.
// The driver must explicitly accept before being assigned.
type RideOfferedEvent struct {
	TripID         string `json:"trip_id"`
	DriverID       string `json:"driver_id"`
	RiderID        string `json:"rider_id"`
	Pickup         LatLng `json:"pickup"`
	Drop           LatLng `json:"drop"`
	VehicleType    string `json:"vehicle_type,omitempty"`
	OfferExpiresAt string `json:"offer_expires_at"`
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
	RiderEmail      string  `json:"rider_email,omitempty"`
	RiderPhone      string  `json:"rider_phone,omitempty"`
	Fare            float64 `json:"fare"`
	PaymentMethod   string  `json:"payment_method,omitempty"`
	CompletedAt     string  `json:"completed_at"`
	DurationSeconds int64   `json:"duration_seconds"`
}

// TripCancelledEvent is published to trip.cancelled.
type TripCancelledEvent struct {
	TripID      string `json:"trip_id"`
	DriverID    string `json:"driver_id,omitempty"`
	RiderID     string `json:"rider_id"`
	RiderEmail  string `json:"rider_email,omitempty"`
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
	RiderEmail  string  `json:"rider_email,omitempty"`
	DriverID    string  `json:"driver_id"`
	Amount      float64 `json:"amount"`
	Status      string  `json:"status,omitempty"`
	CompletedAt string  `json:"completed_at"`
}
