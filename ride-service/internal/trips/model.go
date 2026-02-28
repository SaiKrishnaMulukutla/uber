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
