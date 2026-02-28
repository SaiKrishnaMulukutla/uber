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
