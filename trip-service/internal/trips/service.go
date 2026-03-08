package trips

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"uber/shared/events"
	"uber/shared/pkg/kafka"
	rredis "uber/shared/pkg/redis"
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

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ev := events.RideRequestedEvent{
			TripID:      id,
			RiderID:     riderID,
			Pickup:      events.LatLng{Lat: req.PickupLat, Lng: req.PickupLng},
			Drop:        events.LatLng{Lat: req.DropLat, Lng: req.DropLng},
			RequestedAt: now.Format(time.RFC3339),
		}
		if err := s.kafka.Publish(ctx, kafka.TopicRideRequested, id, ev); err != nil {
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
// Driver status restoration is handled by driver-service via Kafka.
func (s *Service) End(ctx context.Context, tripID string, distKm *float64) (*Trip, error) {
	trip, err := s.GetByID(ctx, tripID)
	if err != nil {
		return nil, err
	}

	km := 0.0
	if distKm != nil && *distKm > 0 {
		km = *distKm
	} else {
		km = haversineKm(trip.PickupLat, trip.PickupLng, trip.DropLat, trip.DropLng)
	}

	fare := 50.0 + km*12.0
	now := time.Now()

	tag, err := s.db.Exec(ctx,
		`UPDATE trips SET status=$1, fare=$2, completed_at=$3 WHERE id=$4 AND status=$5`,
		StatusCompleted, fare, now, tripID, StatusStarted)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, errors.New("trip not in STARTED state")
	}

	driverID := ""
	if trip.DriverID != nil {
		driverID = *trip.DriverID
	}
	var durSec int64
	if trip.StartedAt != nil {
		durSec = int64(now.Sub(*trip.StartedAt).Seconds())
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ev := events.TripCompletedEvent{
			TripID:          tripID,
			DriverID:        driverID,
			RiderID:         trip.RiderID,
			Fare:            fare,
			CompletedAt:     now.Format(time.RFC3339),
			DurationSeconds: durSec,
		}
		if err := s.kafka.Publish(ctx, kafka.TopicTripCompleted, tripID, ev); err != nil {
			log.Printf("[trips] failed to publish trip.completed: %v", err)
		}
		// Restore driver to GEO pool (driver-service handles DB status via Kafka).
		if driverID != "" {
			if lat, lng, locErr := s.redis.GetDriverLocation(ctx, driverID); locErr == nil {
				_ = s.redis.SetDriverLocation(ctx, driverID, lat, lng)
			}
		}
	}()

	return s.GetByID(ctx, tripID)
}

// Cancel transitions a REQUESTED, MATCHING, or DRIVER_ASSIGNED trip to CANCELLED.
// Driver status restoration is handled by driver-service via Kafka.
func (s *Service) Cancel(ctx context.Context, tripID, reason string) (*Trip, error) {
	trip, err := s.GetByID(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if trip.Status != StatusRequested && trip.Status != StatusMatching && trip.Status != StatusDriverAssigned {
		return nil, fmt.Errorf("cannot cancel trip in status %s", trip.Status)
	}

	now := time.Now()
	tag, err := s.db.Exec(ctx,
		`UPDATE trips SET status=$1, completed_at=$2 WHERE id=$3 AND status IN ($4,$5,$6)`,
		StatusCancelled, now, tripID, StatusRequested, StatusMatching, StatusDriverAssigned)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, errors.New("trip already transitioned or not found")
	}

	driverID := ""
	if trip.DriverID != nil {
		driverID = *trip.DriverID
	}

	// Restore driver to GEO pool immediately (driver-service handles DB status via Kafka).
	if driverID != "" {
		if lat, lng, locErr := s.redis.GetDriverLocation(ctx, driverID); locErr == nil {
			_ = s.redis.SetDriverLocation(ctx, driverID, lat, lng)
		} else {
			log.Printf("[trips] cancel: no saved location for driver %s, cannot restore GEO", driverID)
		}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ev := events.TripCancelledEvent{
			TripID:      tripID,
			DriverID:    driverID,
			RiderID:     trip.RiderID,
			Reason:      reason,
			CancelledAt: now.Format(time.RFC3339),
		}
		if err := s.kafka.Publish(ctx, kafka.TopicTripCancelled, tripID, ev); err != nil {
			log.Printf("[trips] failed to publish trip.cancelled: %v", err)
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

// ListByRider returns paginated trips for a rider.
func (s *Service) ListByRider(ctx context.Context, riderID string, limit, offset int) (*HistoryResponse, error) {
	var total int
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM trips WHERE rider_id=$1", riderID).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT id,rider_id,driver_id,pickup_lat,pickup_lng,drop_lat,drop_lng,
		        fare,status,requested_at,started_at,completed_at,created_at
		 FROM trips WHERE rider_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, riderID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	trips := []*Trip{}
	for rows.Next() {
		var t Trip
		if err := rows.Scan(&t.ID, &t.RiderID, &t.DriverID,
			&t.PickupLat, &t.PickupLng, &t.DropLat, &t.DropLng,
			&t.Fare, &t.Status, &t.RequestedAt, &t.StartedAt, &t.CompletedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		trips = append(trips, &t)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &HistoryResponse{Trips: trips, Total: total, Limit: limit, Offset: offset}, nil
}

// ListByDriver returns paginated trips for a driver.
func (s *Service) ListByDriver(ctx context.Context, driverID string, limit, offset int) (*HistoryResponse, error) {
	var total int
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM trips WHERE driver_id=$1", driverID).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT id,rider_id,driver_id,pickup_lat,pickup_lng,drop_lat,drop_lng,
		        fare,status,requested_at,started_at,completed_at,created_at
		 FROM trips WHERE driver_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, driverID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	trips := []*Trip{}
	for rows.Next() {
		var t Trip
		if err := rows.Scan(&t.ID, &t.RiderID, &t.DriverID,
			&t.PickupLat, &t.PickupLng, &t.DropLat, &t.DropLng,
			&t.Fare, &t.Status, &t.RequestedAt, &t.StartedAt, &t.CompletedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		trips = append(trips, &t)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &HistoryResponse{Trips: trips, Total: total, Limit: limit, Offset: offset}, nil
}

// Estimate computes a fare estimate without creating a trip.
func (s *Service) Estimate(pickupLat, pickupLng, dropLat, dropLng float64) *EstimateResponse {
	km := haversineKm(pickupLat, pickupLng, dropLat, dropLng)
	surge := 1.0
	fare := (50.0 + km*12.0) * surge
	durationMin := (km / 25.0) * 60.0
	return &EstimateResponse{
		EstimatedFare:     math.Round(fare*100) / 100,
		EstimatedDistance: math.Round(km*100) / 100,
		EstimatedDuration: math.Round(durationMin*100) / 100,
		SurgeMultiplier:   surge,
		Currency:          "INR",
	}
}

// Rate allows a rider or driver to rate their counterpart after a completed trip.
func (s *Service) Rate(ctx context.Context, tripID, raterID, raterRole string, req RateRequest) (*Rating, error) {
	trip, err := s.GetByID(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if trip.Status != StatusCompleted {
		return nil, errors.New("can only rate completed trips")
	}

	var rateeID, rateeRole string
	switch raterRole {
	case "rider":
		if raterID != trip.RiderID {
			return nil, errors.New("you are not the rider on this trip")
		}
		if trip.DriverID == nil {
			return nil, errors.New("no driver assigned to rate")
		}
		rateeID = *trip.DriverID
		rateeRole = "driver"
	case "driver":
		if trip.DriverID == nil || raterID != *trip.DriverID {
			return nil, errors.New("you are not the driver on this trip")
		}
		rateeID = trip.RiderID
		rateeRole = "rider"
	default:
		return nil, errors.New("invalid role")
	}

	var rating Rating
	err = s.db.QueryRow(ctx,
		`INSERT INTO ratings (trip_id, rater_id, rater_role, ratee_id, ratee_role, score, comment)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 RETURNING id, trip_id, rater_id, rater_role, ratee_id, ratee_role, score, comment, created_at`,
		tripID, raterID, raterRole, rateeID, rateeRole, req.Score, req.Comment).
		Scan(&rating.ID, &rating.TripID, &rating.RaterID, &rating.RaterRole,
			&rating.RateeID, &rating.RateeRole, &rating.Score, &rating.Comment, &rating.CreatedAt)
	if err != nil {
		return nil, errors.New("already rated or database error")
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ev := events.RatingSubmittedEvent{
			TripID:    tripID,
			RaterID:   raterID,
			RaterRole: raterRole,
			RateeID:   rateeID,
			RateeRole: rateeRole,
			Score:     req.Score,
			Comment:   req.Comment,
		}
		if err := s.kafka.Publish(ctx, kafka.TopicRatingSubmitted, tripID, ev); err != nil {
			log.Printf("[trips] failed to publish rating.submitted: %v", err)
		}
	}()

	return &rating, nil
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
