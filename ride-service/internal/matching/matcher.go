package matching

import (
	"context"
	"encoding/json"
	"log"

	"ride-service/internal/events"
	"ride-service/pkg/kafka"
	rredis "ride-service/pkg/redis"
)

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
		var ev events.RideRequestedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}

		log.Printf("[matching] ride.requested → trip=%s rider=%s", ev.TripID, ev.RiderID)

		// Find nearest driver within 5 km
		drivers, err := m.redis.GetNearbyDrivers(ctx, ev.Pickup.Lat, ev.Pickup.Lng, 5.0, 1)
		if err != nil {
			// Redis error — return error so Kafka does NOT commit the offset and retries.
			log.Printf("[matching] redis error for trip %s: %v", ev.TripID, err)
			return err
		}
		if len(drivers) == 0 {
			// No drivers available — expected case, commit offset, wait for manual assign.
			log.Printf("[matching] no nearby drivers for trip %s", ev.TripID)
			return nil
		}

		assigned := events.DriverAssignedEvent{
			TripID:   ev.TripID,
			DriverID: drivers[0],
		}

		if err := m.kafka.Publish(ctx, kafka.TopicDriverAssigned, ev.TripID, assigned); err != nil {
			log.Printf("[matching] failed to publish driver.assigned: %v", err)
			return err
		}

		// Remove driver from available pool so they aren't double-assigned
		_ = m.redis.RemoveDriverLocation(ctx, drivers[0])

		log.Printf("[matching] assigned driver %s → trip %s", drivers[0], ev.TripID)
		return nil
	})
}
