package trips

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ride-service/internal/events"
	"ride-service/pkg/kafka"
	rredis "ride-service/pkg/redis"
)

// Service contains trip business logic.
type Service struct {
	db    *pgxpool.Pool
	kafka *kafka.Client
	redis *rredis.Client
}

// NewService creates a trip service.
func NewService(db *pgxpool.Pool, k *kafka.Client, r *rredis.Client) *Service {
	return &Service{db: db, kafka: k, redis: r}
}

// Request creates a new trip and publishes ride.requested.
func (s *Service) Request(ctx context.Context, riderID string, req TripRequest) (*Trip, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := s.db.Exec(ctx,
		`INSERT INTO trips (id,rider_id,pickup_lat,pickup_lng,drop_lat,drop_lng,status,requested_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, riderID, req.PickupLat, req.PickupLng, req.DropLat, req.DropLng, StatusRequested, now)
	if err != nil {
		return nil, err
	}

	trip := &Trip{
		ID: id, RiderID: riderID,
		PickupLat: req.PickupLat, PickupLng: req.PickupLng,
		DropLat: req.DropLat, DropLng: req.DropLng,
		Status: StatusRequested, RequestedAt: &now, CreatedAt: now,
	}

	// Async Kafka publish
	go func() {
		ev := events.RideRequestedEvent{
			TripID:      id,
			RiderID:     riderID,
			Pickup:      events.LatLng{Lat: req.PickupLat, Lng: req.PickupLng},
			Drop:        events.LatLng{Lat: req.DropLat, Lng: req.DropLng},
			RequestedAt: now.Format(time.RFC3339),
		}
		if err := s.kafka.Publish(context.Background(), kafka.TopicRideRequested, id, ev); err != nil {
			log.Printf("[trips] failed to publish ride.requested: %v", err)
		} else {
			log.Printf("[trips] published ride.requested for trip %s", id)
		}
	}()

	return trip, nil
}

// GetByID fetches a trip by primary key.
func (s *Service) GetByID(ctx context.Context, id string) (*Trip, error) {
	var t Trip
	err := s.db.QueryRow(ctx,
		`SELECT id,rider_id,driver_id,pickup_lat,pickup_lng,drop_lat,drop_lng,
		        fare,status,requested_at,started_at,completed_at,created_at
		 FROM trips WHERE id=$1`, id).
		Scan(&t.ID, &t.RiderID, &t.DriverID,
			&t.PickupLat, &t.PickupLng, &t.DropLat, &t.DropLng,
			&t.Fare, &t.Status, &t.RequestedAt, &t.StartedAt, &t.CompletedAt, &t.CreatedAt)
	if err != nil {
		return nil, errors.New("trip not found")
	}
	return &t, nil
}

// AssignDriver sets the driver on a trip (manual / matching callback).
func (s *Service) AssignDriver(ctx context.Context, tripID, driverID string) (*Trip, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE trips SET driver_id=$1, status=$2
		 WHERE id=$3 AND status IN ($4,$5)`,
		driverID, StatusDriverAssigned, tripID, StatusRequested, StatusMatching)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, errors.New("trip not found or invalid state for assignment")
	}
	return s.GetByID(ctx, tripID)
}

// Start transitions a trip to STARTED.
func (s *Service) Start(ctx context.Context, tripID string) (*Trip, error) {
	now := time.Now()
	tag, err := s.db.Exec(ctx,
		`UPDATE trips SET status=$1, started_at=$2
		 WHERE id=$3 AND status=$4`,
		StatusStarted, now, tripID, StatusDriverAssigned)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, errors.New("trip not found or not in DRIVER_ASSIGNED state")
	}
	return s.GetByID(ctx, tripID)
}

// End completes a trip, computes fare, and publishes trip.completed.
func (s *Service) End(ctx context.Context, tripID string, distKm *float64) (*Trip, error) {
	trip, err := s.GetByID(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if trip.Status != StatusStarted {
		return nil, errors.New("trip not in STARTED state")
	}

	// Compute distance
	km := 0.0
	if distKm != nil && *distKm > 0 {
		km = *distKm
	} else {
		km = haversineKm(trip.PickupLat, trip.PickupLng, trip.DropLat, trip.DropLng)
	}

	// Simple fare: base ₹50 + ₹12/km
	fare := 50.0 + km*12.0
	now := time.Now()

	_, err = s.db.Exec(ctx,
		`UPDATE trips SET status=$1, fare=$2, completed_at=$3 WHERE id=$4`,
		StatusCompleted, fare, now, tripID)
	if err != nil {
		return nil, err
	}

	// Publish trip.completed
	driverID := ""
	if trip.DriverID != nil {
		driverID = *trip.DriverID
	}
	var durSec int64
	if trip.StartedAt != nil {
		durSec = int64(now.Sub(*trip.StartedAt).Seconds())
	}

	go func() {
		ev := events.TripCompletedEvent{
			TripID:          tripID,
			DriverID:        driverID,
			RiderID:         trip.RiderID,
			Fare:            fare,
			CompletedAt:     now.Format(time.RFC3339),
			DurationSeconds: durSec,
		}
		if err := s.kafka.Publish(context.Background(), kafka.TopicTripCompleted, tripID, ev); err != nil {
			log.Printf("[trips] failed to publish trip.completed: %v", err)
		}
	}()

	return s.GetByID(ctx, tripID)
}

// StartDriverAssignedConsumer listens for driver.assigned events from the matching service.
func (s *Service) StartDriverAssignedConsumer(ctx context.Context) {
	s.kafka.Subscribe(ctx, kafka.TopicDriverAssigned, "trip-driver-assigned", func(data []byte) error {
		var ev events.DriverAssignedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[trips] received driver.assigned: trip=%s driver=%s", ev.TripID, ev.DriverID)

		_, err := s.db.Exec(ctx,
			`UPDATE trips SET driver_id=$1, status=$2
			 WHERE id=$3 AND status IN ($4,$5)`,
			ev.DriverID, StatusDriverAssigned, ev.TripID, StatusRequested, StatusMatching)
		return err
	})
}

// ---- helpers ----

func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
