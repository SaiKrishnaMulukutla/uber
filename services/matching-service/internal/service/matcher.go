package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"uber/shared/pkg/kafka"
	rredis "uber/shared/pkg/redis"
)

const (
	offerTTL     = 15 * time.Second
	offerLockTTL = 20 * time.Second // slightly longer than offerTTL to cover processing time
)

type geoClient interface {
	GetNearbyDrivers(ctx context.Context, lat, lng, radiusKm float64, count int) ([]string, error)
	RemoveDriverLocation(ctx context.Context, driverID string) error
	SetDriverLocation(ctx context.Context, driverID string, lat, lng float64) error
	GetDriverLocation(ctx context.Context, driverID string) (float64, float64, error)
	LockDriver(ctx context.Context, driverID string, ttl time.Duration) (bool, error)
	UnlockDriver(ctx context.Context, driverID string) error
	GetDriverGeoPos(ctx context.Context, driverID string) (float64, float64, error)
	SaveDriverLocation(ctx context.Context, driverID string, lat, lng float64) error
	SetOffer(ctx context.Context, tripID, driverID string, ttl time.Duration) error
	GetOffer(ctx context.Context, tripID string) (string, error)
	DeleteOffer(ctx context.Context, tripID string) error
	SetOfferEvent(ctx context.Context, tripID string, data []byte, ttl time.Duration) error
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
// Instead of immediately assigning a driver, it sends a ride offer that the
// driver must explicitly accept within offerTTL seconds.
// Kept as a package-level function for testability with injected mocks.
func handleRideRequested(ctx context.Context, data []byte, pub eventPublisher, geo geoClient) error {
	var ev kafka.RideRequestedEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return err
	}

	log.Printf("[matching] ride.requested → trip=%s rider=%s retry=%d", ev.TripID, ev.RiderID, ev.RetryCount)

	const (
		maxRetries = 5
		retryDelay = 15 * time.Second
	)

	allDrivers, err := geo.GetNearbyDrivers(ctx, ev.Pickup.Lat, ev.Pickup.Lng, 5.0, 10)
	if err != nil {
		log.Printf("[matching] redis error for trip %s: %v", ev.TripID, err)
		return err
	}

	// Filter out drivers who already declined or timed out for this trip.
	skipSet := make(map[string]bool, len(ev.SkipDriverIDs))
	for _, id := range ev.SkipDriverIDs {
		skipSet[id] = true
	}
	drivers := allDrivers[:0]
	for _, d := range allDrivers {
		if !skipSet[d] {
			drivers = append(drivers, d)
		}
	}

	if len(drivers) == 0 {
		if ev.RetryCount >= maxRetries {
			log.Printf("[matching] no drivers after %d retries for trip %s, giving up", maxRetries, ev.TripID)
			return nil
		}
		ev.RetryCount++
		log.Printf("[matching] no drivers for trip %s, retry %d/%d in %s", ev.TripID, ev.RetryCount, maxRetries, retryDelay)
		go func(retryEv kafka.RideRequestedEvent) {
			time.Sleep(retryDelay)
			if pubErr := pub.Publish(context.Background(), kafka.TopicRideRequested, retryEv.TripID, retryEv); pubErr != nil {
				log.Printf("[matching] failed to re-publish ride.requested for trip %s: %v", retryEv.TripID, pubErr)
			}
		}(ev)
		return nil
	}

	var chosenDriver string
	for _, d := range drivers {
		locked, lockErr := geo.LockDriver(ctx, d, offerLockTTL)
		if lockErr != nil {
			log.Printf("[matching] lock error for driver %s: %v", d, lockErr)
			continue
		}
		if !locked {
			continue
		}
		chosenDriver = d
		break
	}

	if chosenDriver == "" {
		log.Printf("[matching] no lockable drivers for trip %s", ev.TripID)
		return nil
	}

	// Save location backup BEFORE removing from GEO pool so we can restore on reject/timeout.
	lat, lng, posErr := geo.GetDriverGeoPos(ctx, chosenDriver)
	if posErr != nil {
		log.Printf("[matching] warn: could not read geo pos for driver %s, unlocking: %v", chosenDriver, posErr)
		_ = geo.UnlockDriver(ctx, chosenDriver)
		return posErr
	}
	if saveErr := geo.SaveDriverLocation(ctx, chosenDriver, lat, lng); saveErr != nil {
		log.Printf("[matching] warn: could not save location for driver %s, unlocking: %v", chosenDriver, saveErr)
		_ = geo.UnlockDriver(ctx, chosenDriver)
		return saveErr
	}

	// Remove from GEO pool while offer is pending to prevent double-matching.
	_ = geo.RemoveDriverLocation(ctx, chosenDriver)

	// Persist the offer so driver-service can validate the response.
	if err := geo.SetOffer(ctx, ev.TripID, chosenDriver, offerTTL); err != nil {
		log.Printf("[matching] failed to store offer for trip %s: %v", ev.TripID, err)
		_ = geo.UnlockDriver(ctx, chosenDriver)
		_ = geo.SetDriverLocation(ctx, chosenDriver, lat, lng)
		return err
	}

	// Store the original event so driver-service can re-queue on rejection.
	eventData, _ := json.Marshal(ev)
	if storeErr := geo.SetOfferEvent(ctx, ev.TripID, eventData, offerTTL+5*time.Second); storeErr != nil {
		log.Printf("[matching] warn: could not store offer event for trip %s: %v", ev.TripID, storeErr)
		// Non-fatal: the timeout goroutine below will still handle cleanup correctly.
	}

	offered := kafka.RideOfferedEvent{
		TripID:         ev.TripID,
		DriverID:       chosenDriver,
		RiderID:        ev.RiderID,
		Pickup:         ev.Pickup,
		Drop:           ev.Drop,
		OfferExpiresAt: time.Now().UTC().Add(offerTTL).Format(time.RFC3339),
	}

	if err := pub.Publish(ctx, kafka.TopicRideOffered, ev.TripID, offered); err != nil {
		log.Printf("[matching] failed to publish ride.offered: %v", err)
		_ = geo.UnlockDriver(ctx, chosenDriver)
		_ = geo.DeleteOffer(ctx, ev.TripID)
		_ = geo.SetDriverLocation(ctx, chosenDriver, lat, lng)
		return err
	}

	log.Printf("[matching] offered trip %s to driver %s (expires in %s)", ev.TripID, chosenDriver, offerTTL)

	// After offerTTL, if the driver hasn't responded, restore them to the pool
	// and re-queue the trip for the next candidate.
	go func(tripID, driverID string, origEvent kafka.RideRequestedEvent, dLat, dLng float64) {
		time.Sleep(offerTTL)
		stored, err := geo.GetOffer(context.Background(), tripID)
		if err != nil || stored != driverID {
			return // offer was already consumed (accepted or rejected)
		}
		log.Printf("[matching] offer timed out: trip=%s driver=%s — retrying", tripID, driverID)
		_ = geo.DeleteOffer(context.Background(), tripID)
		_ = geo.UnlockDriver(context.Background(), driverID)
		_ = geo.SetDriverLocation(context.Background(), driverID, dLat, dLng)
		origEvent.SkipDriverIDs = append(origEvent.SkipDriverIDs, driverID)
		if pubErr := pub.Publish(context.Background(), kafka.TopicRideRequested, tripID, origEvent); pubErr != nil {
			log.Printf("[matching] failed to re-publish after timeout for trip %s: %v", tripID, pubErr)
		}
	}(ev.TripID, chosenDriver, ev, lat, lng)

	return nil
}
