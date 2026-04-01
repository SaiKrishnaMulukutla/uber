package model

import "time"

// Trip lifecycle statuses.
const (
	StatusRequested      = "REQUESTED"
	StatusDriverAssigned = "DRIVER_ASSIGNED"
	StatusStarted        = "STARTED"
	StatusCompleted      = "COMPLETED"
	StatusCancelled      = "CANCELLED"
)

// Trip represents a ride in the system.
type Trip struct {
	ID          string     `json:"id"`
	RiderID     string     `json:"rider_id"`
	RiderEmail  string     `json:"rider_email,omitempty"`
	DriverID    *string    `json:"driver_id,omitempty"`
	PickupLat   float64    `json:"pickup_lat"`
	PickupLng   float64    `json:"pickup_lng"`
	DropLat     float64    `json:"drop_lat"`
	DropLng     float64    `json:"drop_lng"`
	Fare        *float64   `json:"fare,omitempty"`
	Status      string     `json:"status"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	DurationSeconds *int64     `json:"duration_seconds,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
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

type TripRequest struct {
	PickupLat float64 `json:"pickupLat"`
	PickupLng float64 `json:"pickupLng"`
	DropLat   float64 `json:"dropLat"`
	DropLng   float64 `json:"dropLng"`
}

type AssignRequest struct {
	DriverID string `json:"driverId"`
}

type EndRequest struct {
	DistanceKm      *float64 `json:"distanceKm,omitempty"`
	DurationSeconds *int64   `json:"durationSeconds,omitempty"`
}

type CancelRequest struct {
	Reason string `json:"reason,omitempty"`
}

type HistoryResponse struct {
	Trips  []*Trip `json:"trips"`
	Total  int     `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}

type EstimateRequest struct {
	PickupLat float64 `json:"pickupLat"`
	PickupLng float64 `json:"pickupLng"`
	DropLat   float64 `json:"dropLat"`
	DropLng   float64 `json:"dropLng"`
}

type EstimateResponse struct {
	EstimatedFare     float64 `json:"estimated_fare"`
	EstimatedDistance float64 `json:"estimated_distance_km"`
	EstimatedDuration float64 `json:"estimated_duration_min"`
	SurgeMultiplier   float64 `json:"surge_multiplier"`
	Currency          string  `json:"currency"`
}

type RateRequest struct {
	Score   int    `json:"score"`
	Comment string `json:"comment,omitempty"`
}
