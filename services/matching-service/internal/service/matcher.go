package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"uber/shared/pkg/kafka"
	rredis "uber/shared/pkg/redis"
)

// geoClient abstracts Redis GEO operations needed by the matcher.
type geoClient interface {
	GetNearbyDrivers(ctx context.Context, lat, lng, radiusKm float64, count int) ([]string, error)
	RemoveDriverLocation(ctx context.Context, driverID string) error
	LockDriver(ctx context.Context, driverID string, ttl time.Duration) (bool, error)
	UnlockDriver(ctx context.Context, driverID string) error
	GetDriverGeoPos(ctx context.Context, driverID string) (float64, float64, error)
	SaveDriverLocation(ctx context.Context, driverID string, lat, lng float64) error
}

// eventPublisher abstracts Kafka publish needed by the matcher.
type eventPublisher interface {
	Publish(ctx context.Context, topic, key string, value any) error
}

// Matcher wraps the ride-matching logic.
type Matcher struct {
	kafka *kafka.Client
	redis *rredis.Client
}

// NewMatcher creates a new Matcher.
func NewMatcher(k *kafka.Client, r *rredis.Client) *Matcher {
	return &Matcher{kafka: k, redis: r}
}

// HandleRideRequested processes a raw ride.requested event payload.
func (m *Matcher) HandleRideRequested(ctx context.Context, data []byte) error {
	return handleRideRequested(ctx, data, m.kafka, m.redis)
}

// handleRideRequested processes a single ride.requested event.
// Kept as a package-level function for testability with injected mocks.
func handleRideRequested(ctx context.Context, data []byte, pub eventPublisher, geo geoClient) error {
	var ev kafka.RideRequestedEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return err
	}

	log.Printf("[matching] ride.requested → trip=%s rider=%s", ev.TripID, ev.RiderID)

	drivers, err := geo.GetNearbyDrivers(ctx, ev.Pickup.Lat, ev.Pickup.Lng, 5.0, 5)
	if err != nil {
		log.Printf("[matching] redis error for trip %s: %v", ev.TripID, err)
		return err
	}
	if len(drivers) == 0 {
		log.Printf("[matching] no nearby drivers for trip %s", ev.TripID)
		return nil
	}

	var assignedDriver string
	for _, d := range drivers {
		locked, lockErr := geo.LockDriver(ctx, d, 30*time.Second)
		if lockErr != nil {
			log.Printf("[matching] lock error for driver %s: %v", d, lockErr)
			continue
		}
		if !locked {
			continue
		}
		assignedDriver = d
		break
	}

	if assignedDriver == "" {
		log.Printf("[matching] no available (lockable) drivers for trip %s", ev.TripID)
		return nil
	}

	// Save location backup BEFORE publishing so a failure here doesn't leave an
	// unretractable driver.assigned event in Kafka (which would cause an infinite
	// ride.requested retry loop on the next delivery).
	lat, lng, posErr := geo.GetDriverGeoPos(ctx, assignedDriver)
	if posErr != nil {
		log.Printf("[matching] warn: could not read geo pos for driver %s, unlocking: %v", assignedDriver, posErr)
		_ = geo.UnlockDriver(ctx, assignedDriver)
		return posErr
	}
	if saveErr := geo.SaveDriverLocation(ctx, assignedDriver, lat, lng); saveErr != nil {
		log.Printf("[matching] warn: could not save location for driver %s, unlocking: %v", assignedDriver, saveErr)
		_ = geo.UnlockDriver(ctx, assignedDriver)
		return saveErr
	}

	assigned := kafka.DriverAssignedEvent{
		TripID:   ev.TripID,
		DriverID: assignedDriver,
	}

	if err := pub.Publish(ctx, kafka.TopicDriverAssigned, ev.TripID, assigned); err != nil {
		log.Printf("[matching] failed to publish driver.assigned: %v", err)
		_ = geo.UnlockDriver(ctx, assignedDriver)
		return err
	}

	_ = geo.RemoveDriverLocation(ctx, assignedDriver)

	log.Printf("[matching] assigned driver %s → trip %s", assignedDriver, ev.TripID)
	return nil
}
