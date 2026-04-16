package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"uber/shared/pkg/kafka"
)

type mockGeo struct {
	GetNearbyFn         func(ctx context.Context, lat, lng, radiusKm float64, count int) ([]string, error)
	RemoveFn            func(ctx context.Context, driverID string) error
	SetDriverLocationFn func(ctx context.Context, driverID string, lat, lng float64) error
	GetDriverLocationFn func(ctx context.Context, driverID string) (float64, float64, error)
	LockDriverFn        func(ctx context.Context, driverID string, ttl time.Duration) (bool, error)
	UnlockDriverFn      func(ctx context.Context, driverID string) error
	GetDriverGeoPosFn   func(ctx context.Context, driverID string) (float64, float64, error)
	SaveDriverLocFn     func(ctx context.Context, driverID string, lat, lng float64) error
	SetOfferFn          func(ctx context.Context, tripID, driverID string, ttl time.Duration) error
	GetOfferFn          func(ctx context.Context, tripID string) (string, error)
	DeleteOfferFn       func(ctx context.Context, tripID string) error
	SetOfferEventFn     func(ctx context.Context, tripID string, data []byte, ttl time.Duration) error
	GetDriverTypeFn     func(ctx context.Context, driverID string) string
}

func (m *mockGeo) GetNearbyDrivers(ctx context.Context, lat, lng, radiusKm float64, count int) ([]string, error) {
	return m.GetNearbyFn(ctx, lat, lng, radiusKm, count)
}
func (m *mockGeo) RemoveDriverLocation(ctx context.Context, driverID string) error {
	return m.RemoveFn(ctx, driverID)
}
func (m *mockGeo) SetDriverLocation(ctx context.Context, driverID string, lat, lng float64) error {
	if m.SetDriverLocationFn != nil {
		return m.SetDriverLocationFn(ctx, driverID, lat, lng)
	}
	return nil
}
func (m *mockGeo) GetDriverLocation(ctx context.Context, driverID string) (float64, float64, error) {
	if m.GetDriverLocationFn != nil {
		return m.GetDriverLocationFn(ctx, driverID)
	}
	return 12.97, 77.59, nil
}
func (m *mockGeo) LockDriver(ctx context.Context, driverID string, ttl time.Duration) (bool, error) {
	if m.LockDriverFn != nil {
		return m.LockDriverFn(ctx, driverID, ttl)
	}
	return true, nil
}
func (m *mockGeo) UnlockDriver(ctx context.Context, driverID string) error {
	if m.UnlockDriverFn != nil {
		return m.UnlockDriverFn(ctx, driverID)
	}
	return nil
}
func (m *mockGeo) GetDriverGeoPos(ctx context.Context, driverID string) (float64, float64, error) {
	if m.GetDriverGeoPosFn != nil {
		return m.GetDriverGeoPosFn(ctx, driverID)
	}
	return 12.97, 77.59, nil
}
func (m *mockGeo) SaveDriverLocation(ctx context.Context, driverID string, lat, lng float64) error {
	if m.SaveDriverLocFn != nil {
		return m.SaveDriverLocFn(ctx, driverID, lat, lng)
	}
	return nil
}
func (m *mockGeo) SetOffer(ctx context.Context, tripID, driverID string, ttl time.Duration) error {
	if m.SetOfferFn != nil {
		return m.SetOfferFn(ctx, tripID, driverID, ttl)
	}
	return nil
}
func (m *mockGeo) GetOffer(ctx context.Context, tripID string) (string, error) {
	if m.GetOfferFn != nil {
		return m.GetOfferFn(ctx, tripID)
	}
	return "", errors.New("not found")
}
func (m *mockGeo) DeleteOffer(ctx context.Context, tripID string) error {
	if m.DeleteOfferFn != nil {
		return m.DeleteOfferFn(ctx, tripID)
	}
	return nil
}
func (m *mockGeo) SetOfferEvent(ctx context.Context, tripID string, data []byte, ttl time.Duration) error {
	if m.SetOfferEventFn != nil {
		return m.SetOfferEventFn(ctx, tripID, data, ttl)
	}
	return nil
}
func (m *mockGeo) GetDriverType(ctx context.Context, driverID string) string {
	if m.GetDriverTypeFn != nil {
		return m.GetDriverTypeFn(ctx, driverID)
	}
	return ""
}

type mockPublisher struct {
	PublishFn func(ctx context.Context, topic, key string, value any) error
}

func (m *mockPublisher) Publish(ctx context.Context, topic, key string, value any) error {
	return m.PublishFn(ctx, topic, key, value)
}

func rideRequestedPayload(tripID, riderID string, lat, lng float64) []byte {
	ev := kafka.RideRequestedEvent{
		TripID:  tripID,
		RiderID: riderID,
		Pickup:  kafka.LatLng{Lat: lat, Lng: lng},
		Drop:    kafka.LatLng{Lat: lat + 0.01, Lng: lng + 0.01},
	}
	data, _ := json.Marshal(ev)
	return data
}

func TestHandleRideRequested_OfferSentToDriver(t *testing.T) {
	var publishedTopic, publishedKey string
	var publishedOffer kafka.RideOfferedEvent
	var removedDriver string

	geo := &mockGeo{
		GetNearbyFn: func(_ context.Context, lat, lng, radiusKm float64, count int) ([]string, error) {
			return []string{"driver-1"}, nil
		},
		RemoveFn: func(_ context.Context, driverID string) error {
			removedDriver = driverID
			return nil
		},
	}
	pub := &mockPublisher{
		PublishFn: func(_ context.Context, topic, key string, value any) error {
			publishedTopic = topic
			publishedKey = key
			data, _ := json.Marshal(value)
			json.Unmarshal(data, &publishedOffer)
			return nil
		},
	}

	err := handleRideRequested(context.Background(), rideRequestedPayload("trip-1", "rider-1", 12.97, 77.59), pub, geo)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if publishedTopic != kafka.TopicRideOffered {
		t.Errorf("expected topic %s, got %s", kafka.TopicRideOffered, publishedTopic)
	}
	if publishedKey != "trip-1" {
		t.Errorf("expected key trip-1, got %s", publishedKey)
	}
	if publishedOffer.TripID != "trip-1" {
		t.Errorf("expected TripID trip-1, got %s", publishedOffer.TripID)
	}
	if publishedOffer.DriverID != "driver-1" {
		t.Errorf("expected DriverID driver-1, got %s", publishedOffer.DriverID)
	}
	if removedDriver != "driver-1" {
		t.Errorf("expected driver-1 removed from GEO pool while pending, got %s", removedDriver)
	}
}

func TestHandleRideRequested_NoNearbyDrivers(t *testing.T) {
	publishCalled := false

	geo := &mockGeo{
		GetNearbyFn: func(_ context.Context, _, _, _ float64, _ int) ([]string, error) {
			return []string{}, nil
		},
		RemoveFn: func(_ context.Context, _ string) error { return nil },
	}
	pub := &mockPublisher{
		PublishFn: func(_ context.Context, _, _ string, _ any) error {
			publishCalled = true
			return nil
		},
	}

	err := handleRideRequested(context.Background(), rideRequestedPayload("trip-2", "rider-2", 12.97, 77.59), pub, geo)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if publishCalled {
		t.Error("expected no Kafka publish when no drivers available")
	}
}

func TestHandleRideRequested_RedisError(t *testing.T) {
	geo := &mockGeo{
		GetNearbyFn: func(_ context.Context, _, _, _ float64, _ int) ([]string, error) {
			return nil, errors.New("redis unavailable")
		},
		RemoveFn: func(_ context.Context, _ string) error { return nil },
	}
	pub := &mockPublisher{
		PublishFn: func(_ context.Context, _, _ string, _ any) error { return nil },
	}

	err := handleRideRequested(context.Background(), rideRequestedPayload("trip-3", "rider-3", 12.97, 77.59), pub, geo)

	if err == nil {
		t.Fatal("expected error on Redis failure, got nil")
	}
}

func TestHandleRideRequested_KafkaPublishError(t *testing.T) {
	var unlocked string
	var restoredDriver string

	geo := &mockGeo{
		GetNearbyFn: func(_ context.Context, _, _, _ float64, _ int) ([]string, error) {
			return []string{"driver-1"}, nil
		},
		RemoveFn: func(_ context.Context, _ string) error { return nil },
		UnlockDriverFn: func(_ context.Context, driverID string) error {
			unlocked = driverID
			return nil
		},
		SetDriverLocationFn: func(_ context.Context, driverID string, _, _ float64) error {
			restoredDriver = driverID
			return nil
		},
	}
	pub := &mockPublisher{
		PublishFn: func(_ context.Context, _, _ string, _ any) error {
			return errors.New("kafka broker down")
		},
	}

	err := handleRideRequested(context.Background(), rideRequestedPayload("trip-4", "rider-4", 12.97, 77.59), pub, geo)

	if err == nil {
		t.Fatal("expected error on Kafka publish failure, got nil")
	}
	if unlocked != "driver-1" {
		t.Errorf("expected driver-1 to be unlocked on publish failure, got %q", unlocked)
	}
	if restoredDriver != "driver-1" {
		t.Errorf("expected driver-1 to be restored to GEO on publish failure, got %q", restoredDriver)
	}
}

func TestHandleRideRequested_InvalidPayload(t *testing.T) {
	geo := &mockGeo{
		GetNearbyFn: func(_ context.Context, _, _, _ float64, _ int) ([]string, error) {
			return []string{"driver-1"}, nil
		},
		RemoveFn: func(_ context.Context, _ string) error { return nil },
	}
	pub := &mockPublisher{
		PublishFn: func(_ context.Context, _, _ string, _ any) error { return nil },
	}

	err := handleRideRequested(context.Background(), []byte("not-valid-json"), pub, geo)

	if err == nil {
		t.Fatal("expected error on invalid JSON payload, got nil")
	}
}

func TestHandleRideRequested_PickupCoordinatesPassedToRedis(t *testing.T) {
	var capturedLat, capturedLng float64
	var capturedRadius float64

	geo := &mockGeo{
		GetNearbyFn: func(_ context.Context, lat, lng, radius float64, _ int) ([]string, error) {
			capturedLat = lat
			capturedLng = lng
			capturedRadius = radius
			return []string{}, nil
		},
		RemoveFn: func(_ context.Context, _ string) error { return nil },
	}
	pub := &mockPublisher{
		PublishFn: func(_ context.Context, _, _ string, _ any) error { return nil },
	}

	err := handleRideRequested(context.Background(), rideRequestedPayload("trip-5", "rider-5", 12.97, 77.59), pub, geo)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedLat != 12.97 {
		t.Errorf("expected lat 12.97, got %v", capturedLat)
	}
	if capturedLng != 77.59 {
		t.Errorf("expected lng 77.59, got %v", capturedLng)
	}
	if capturedRadius != 5.0 {
		t.Errorf("expected radius 5.0, got %v", capturedRadius)
	}
}

func TestHandleRideRequested_FirstDriverChosen(t *testing.T) {
	var offeredDriver string

	geo := &mockGeo{
		GetNearbyFn: func(_ context.Context, _, _, _ float64, _ int) ([]string, error) {
			return []string{"nearest-driver", "far-driver"}, nil
		},
		RemoveFn: func(_ context.Context, driverID string) error { return nil },
	}
	pub := &mockPublisher{
		PublishFn: func(_ context.Context, _, _ string, value any) error {
			data, _ := json.Marshal(value)
			var ev kafka.RideOfferedEvent
			json.Unmarshal(data, &ev)
			offeredDriver = ev.DriverID
			return nil
		},
	}

	err := handleRideRequested(context.Background(), rideRequestedPayload("trip-6", "rider-6", 12.97, 77.59), pub, geo)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offeredDriver != "nearest-driver" {
		t.Errorf("expected nearest-driver to receive offer, got %s", offeredDriver)
	}
}

func TestHandleRideRequested_SkipDriverIDs(t *testing.T) {
	var offeredDriver string

	geo := &mockGeo{
		GetNearbyFn: func(_ context.Context, _, _, _ float64, _ int) ([]string, error) {
			return []string{"driver-A", "driver-B"}, nil
		},
		RemoveFn: func(_ context.Context, _ string) error { return nil },
	}
	pub := &mockPublisher{
		PublishFn: func(_ context.Context, _, _ string, value any) error {
			data, _ := json.Marshal(value)
			var ev kafka.RideOfferedEvent
			json.Unmarshal(data, &ev)
			offeredDriver = ev.DriverID
			return nil
		},
	}

	ev := kafka.RideRequestedEvent{
		TripID:        "trip-7",
		RiderID:       "rider-7",
		Pickup:        kafka.LatLng{Lat: 12.97, Lng: 77.59},
		Drop:          kafka.LatLng{Lat: 12.98, Lng: 77.60},
		SkipDriverIDs: []string{"driver-A"}, // already rejected
	}
	data, _ := json.Marshal(ev)

	err := handleRideRequested(context.Background(), data, pub, geo)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offeredDriver != "driver-B" {
		t.Errorf("expected driver-B (since driver-A is skipped), got %s", offeredDriver)
	}
}
