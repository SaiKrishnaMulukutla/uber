package trips

import "time"

// TripStatus enumerates the lifecycle states.
const (
	StatusRequested      = "REQUESTED"
	StatusMatching       = "MATCHING"
	StatusDriverAssigned = "DRIVER_ASSIGNED"
	StatusStarted        = "STARTED"
	StatusCompleted      = "COMPLETED"
	StatusCancelled      = "CANCELLED"
)

// Trip represents a ride in the system.
type Trip struct {
	ID          string     `json:"id"`
	RiderID     string     `json:"rider_id"`
	DriverID    *string    `json:"driver_id,omitempty"`
	PickupLat   float64    `json:"pickup_lat"`
	PickupLng   float64    `json:"pickup_lng"`
	DropLat     float64    `json:"drop_lat"`
	DropLng     float64    `json:"drop_lng"`
	Fare        *float64   `json:"fare,omitempty"`
	Status      string     `json:"status"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// TripRequest is the body for POST /trips/request.
type TripRequest struct {
	PickupLat float64 `json:"pickupLat"`
	PickupLng float64 `json:"pickupLng"`
	DropLat   float64 `json:"dropLat"`
	DropLng   float64 `json:"dropLng"`
}

// AssignRequest is the body for PATCH /trips/:id/assign.
type AssignRequest struct {
	DriverID string `json:"driverId"`
}

// EndRequest is the optional body for PATCH /trips/:id/end.
type EndRequest struct {
	DistanceKm      *float64 `json:"distanceKm,omitempty"`
	DurationSeconds *int64   `json:"durationSeconds,omitempty"`
}

// CancelRequest is the optional body for PATCH /trips/:id/cancel.
type CancelRequest struct {
	Reason string `json:"reason,omitempty"`
}

// HistoryResponse is the paginated response for GET /trips/history.
type HistoryResponse struct {
	Trips  []*Trip `json:"trips"`
	Total  int     `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}

// EstimateRequest is the body for POST /trips/estimate.
type EstimateRequest struct {
	PickupLat float64 `json:"pickupLat"`
	PickupLng float64 `json:"pickupLng"`
	DropLat   float64 `json:"dropLat"`
	DropLng   float64 `json:"dropLng"`
}

// EstimateResponse is returned by POST /trips/estimate.
type EstimateResponse struct {
	EstimatedFare     float64 `json:"estimated_fare"`
	EstimatedDistance float64 `json:"estimated_distance_km"`
	EstimatedDuration float64 `json:"estimated_duration_min"`
	SurgeMultiplier   float64 `json:"surge_multiplier"`
	Currency          string  `json:"currency"`
}

// Rating represents a trip rating.
type Rating struct {
	ID        string    `json:"id"`
	TripID    string    `json:"trip_id"`
	RaterID   string    `json:"rater_id"`
	RaterRole string    `json:"rater_role"`
	RateeID   string    `json:"ratee_id"`
	RateeRole string    `json:"ratee_role"`
	Score     int       `json:"score"`
	Comment   string    `json:"comment,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// RateRequest is the body for POST /trips/{id}/rate.
type RateRequest struct {
	Score   int    `json:"score"`
	Comment string `json:"comment,omitempty"`
}
