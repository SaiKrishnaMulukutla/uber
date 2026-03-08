package matching

import (
	"context"
	"encoding/json"
	"log"

	"uber/shared/events"
	"uber/shared/pkg/kafka"
	rredis "uber/shared/pkg/redis"
)

// geoClient abstracts Redis GEO operations needed by the matcher.
type geoClient interface {
	GetNearbyDrivers(ctx context.Context, lat, lng, radiusKm float64, count int) ([]string, error)
	RemoveDriverLocation(ctx context.Context, driverID string) error
}

// eventPublisher abstracts Kafka publish needed by the matcher.
type eventPublisher interface {
	Publish(ctx context.Context, topic, key string, value any) error
}

// Matcher consumes ride.requested events, finds the nearest driver,
// and publishes driver.assigned.
type Matcher struct {
	kafka *kafka.Client
	redis *rredis.Client
}

// NewMatcher creates a new matcher.
func NewMatcher(k *kafka.Client, r *rredis.Client) *Matcher {
	return &Matcher{kafka: k, redis: r}
}

// Start begins consuming ride.requested in a background goroutine.
func (m *Matcher) Start(ctx context.Context) {
	m.kafka.Subscribe(ctx, kafka.TopicRideRequested, "matching-group", func(data []byte) error {
		return handleRideRequested(ctx, data, m.kafka, m.redis)
	})
}

// handleRideRequested processes a single ride.requested event payload.
// Extracted for testability.
func handleRideRequested(ctx context.Context, data []byte, pub eventPublisher, geo geoClient) error {
	var ev events.RideRequestedEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return err
	}

	log.Printf("[matching] ride.requested → trip=%s rider=%s", ev.TripID, ev.RiderID)

	// Find nearest driver within 5 km
	drivers, err := geo.GetNearbyDrivers(ctx, ev.Pickup.Lat, ev.Pickup.Lng, 5.0, 1)
	if err != nil {
		log.Printf("[matching] redis error for trip %s: %v", ev.TripID, err)
		return err
	}
	if len(drivers) == 0 {
		log.Printf("[matching] no nearby drivers for trip %s", ev.TripID)
		return nil
	}

	assigned := events.DriverAssignedEvent{
		TripID:   ev.TripID,
		DriverID: drivers[0],
	}

	if err := pub.Publish(ctx, kafka.TopicDriverAssigned, ev.TripID, assigned); err != nil {
		log.Printf("[matching] failed to publish driver.assigned: %v", err)
		return err
	}

	// Remove driver from available pool so they aren't double-assigned
	_ = geo.RemoveDriverLocation(ctx, drivers[0])

	log.Printf("[matching] assigned driver %s → trip %s", drivers[0], ev.TripID)
	return nil
}
