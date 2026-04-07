package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/google/uuid"

	"uber/shared/pkg/kafka"
	"uber/trip-service/internal/model"
	"uber/trip-service/internal/repositories"
	rredis "uber/shared/pkg/redis"
)

// TripService defines trip business operations.
type TripService interface {
	Request(ctx context.Context, riderID, riderEmail string, req model.TripRequest) (*model.Trip, error)
	GetByID(ctx context.Context, id string) (*model.Trip, error)
	AssignDriver(ctx context.Context, tripID, driverID string) (*model.Trip, error)
	Start(ctx context.Context, tripID string) (*model.Trip, error)
	End(ctx context.Context, tripID string, distKm *float64) (*model.Trip, error)
	Cancel(ctx context.Context, tripID, reason string) (*model.Trip, error)
	ListByRider(ctx context.Context, riderID string, limit, offset int) (*model.HistoryResponse, error)
	ListByDriver(ctx context.Context, driverID string, limit, offset int) (*model.HistoryResponse, error)
	Estimate(ctx context.Context, pickupLat, pickupLng, dropLat, dropLng float64) *model.EstimateResponse
	Rate(ctx context.Context, tripID, raterID, raterRole string, req model.RateRequest) (*model.Rating, error)
	PushLocation(ctx context.Context, tripID, driverID string, lat, lng float64) error
}

type tripService struct {
	repo  repositories.TripRepository
	kafka *kafka.Client
	redis *rredis.Client
}

// New returns a TripService backed by the given dependencies.
func New(repo repositories.TripRepository, k *kafka.Client, r *rredis.Client) TripService {
	return &tripService{repo: repo, kafka: k, redis: r}
}

func (s *tripService) Request(ctx context.Context, riderID, riderEmail string, req model.TripRequest) (*model.Trip, error) {
	id := uuid.New().String()
	now := time.Now()

	pm := req.PaymentMethod
	if pm == "" {
		pm = "cash"
	}

	t := &model.Trip{
		ID:            id,
		RiderID:       riderID,
		RiderEmail:    riderEmail,
		PickupLat:     req.PickupLat,
		PickupLng:     req.PickupLng,
		DropLat:       req.DropLat,
		DropLng:       req.DropLng,
		Status:        model.StatusRequested,
		PaymentMethod: pm,
		RequestedAt:   &now,
		CreatedAt:     now,
	}

	if err := s.repo.CreateTrip(ctx, t); err != nil {
		return nil, err
	}

	ev := kafka.RideRequestedEvent{
		TripID:      id,
		RiderID:     riderID,
		Pickup:      kafka.LatLng{Lat: req.PickupLat, Lng: req.PickupLng},
		Drop:        kafka.LatLng{Lat: req.DropLat, Lng: req.DropLng},
		RequestedAt: now.Format(time.RFC3339),
	}
	if err := s.kafka.Publish(ctx, kafka.TopicRideRequested, id, ev); err != nil {
		log.Printf("[trip-service] failed to publish ride.requested: %v", err)
	}

	return t, nil
}

func (s *tripService) GetByID(ctx context.Context, id string) (*model.Trip, error) {
	return s.repo.FindByID(ctx, id)
}

func (s *tripService) AssignDriver(ctx context.Context, tripID, driverID string) (*model.Trip, error) {
	if err := s.repo.AssignDriver(ctx, tripID, driverID); err != nil {
		return nil, err
	}
	return s.repo.FindByID(ctx, tripID)
}

func (s *tripService) Start(ctx context.Context, tripID string) (*model.Trip, error) {
	now := time.Now()
	if err := s.repo.StartTrip(ctx, tripID, now); err != nil {
		return nil, err
	}
	return s.repo.FindByID(ctx, tripID)
}

func (s *tripService) End(ctx context.Context, tripID string, distKm *float64) (*model.Trip, error) {
	trip, err := s.repo.FindByID(ctx, tripID)
	if err != nil {
		return nil, err
	}

	km := 0.0
	if distKm != nil && *distKm > 0 {
		km = *distKm
	} else {
		km = haversineKm(trip.PickupLat, trip.PickupLng, trip.DropLat, trip.DropLng)
	}

	fare := calcFare(km)
	now := time.Now()

	var durSec int64
	if trip.StartedAt != nil {
		durSec = int64(now.Sub(*trip.StartedAt).Seconds())
	}

	if err := s.repo.EndTrip(ctx, tripID, fare, durSec, now); err != nil {
		return nil, err
	}

	driverID := ""
	if trip.DriverID != nil {
		driverID = *trip.DriverID
	}

	ev := kafka.TripCompletedEvent{
		TripID:          tripID,
		DriverID:        driverID,
		RiderID:         trip.RiderID,
		RiderEmail:      trip.RiderEmail,
		Fare:            fare,
		PaymentMethod:   trip.PaymentMethod,
		CompletedAt:     now.Format(time.RFC3339),
		DurationSeconds: durSec,
	}
	if err := s.kafka.Publish(ctx, kafka.TopicTripCompleted, tripID, ev); err != nil {
		log.Printf("[trip-service] failed to publish trip.completed: %v", err)
	}

	if driverID != "" {
		if lat, lng, locErr := s.redis.GetDriverLocation(ctx, driverID); locErr == nil {
			_ = s.redis.SetDriverLocation(ctx, driverID, lat, lng)
		} else {
			log.Printf("warn: could not restore driver %s to GEO after trip end: %v", driverID, locErr)
		}
	}

	return s.repo.FindByID(ctx, tripID)
}

func (s *tripService) Cancel(ctx context.Context, tripID, reason string) (*model.Trip, error) {
	trip, err := s.repo.FindByID(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if trip.Status != model.StatusRequested &&
		trip.Status != model.StatusDriverAssigned {
		return nil, fmt.Errorf("cannot cancel trip in status %s", trip.Status)
	}

	now := time.Now()
	if err := s.repo.CancelTrip(ctx, tripID, now); err != nil {
		return nil, err
	}

	driverID := ""
	if trip.DriverID != nil {
		driverID = *trip.DriverID
	}

	if driverID != "" {
		if lat, lng, locErr := s.redis.GetDriverLocation(ctx, driverID); locErr == nil {
			_ = s.redis.SetDriverLocation(ctx, driverID, lat, lng)
		} else {
			log.Printf("[trip-service] cancel: no saved location for driver %s", driverID)
		}
	}

	ev := kafka.TripCancelledEvent{
		TripID:      tripID,
		DriverID:    driverID,
		RiderID:     trip.RiderID,
		RiderEmail:  trip.RiderEmail,
		Reason:      reason,
		CancelledAt: now.Format(time.RFC3339),
	}
	if err := s.kafka.Publish(ctx, kafka.TopicTripCancelled, tripID, ev); err != nil {
		log.Printf("[trip-service] failed to publish trip.cancelled: %v", err)
	}

	return s.repo.FindByID(ctx, tripID)
}

func (s *tripService) ListByRider(ctx context.Context, riderID string, limit, offset int) (*model.HistoryResponse, error) {
	trips, total, err := s.repo.ListByRider(ctx, riderID, limit, offset)
	if err != nil {
		return nil, err
	}
	return &model.HistoryResponse{Trips: trips, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *tripService) ListByDriver(ctx context.Context, driverID string, limit, offset int) (*model.HistoryResponse, error) {
	trips, total, err := s.repo.ListByDriver(ctx, driverID, limit, offset)
	if err != nil {
		return nil, err
	}
	return &model.HistoryResponse{Trips: trips, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *tripService) Estimate(ctx context.Context, pickupLat, pickupLng, dropLat, dropLng float64) *model.EstimateResponse {
	km := haversineKm(pickupLat, pickupLng, dropLat, dropLng)
	surge := s.redis.GetSurge(ctx)
	fare := calcFare(km) * surge
	durationMin := calcDurationMin(km)
	return &model.EstimateResponse{
		EstimatedFare:     math.Round(fare*100) / 100,
		EstimatedDistance: math.Round(km*100) / 100,
		EstimatedDuration: math.Round(durationMin*100) / 100,
		SurgeMultiplier:   surge,
		Currency:          "INR",
	}
}

func (s *tripService) Rate(ctx context.Context, tripID, raterID, raterRole string, req model.RateRequest) (*model.Rating, error) {
	trip, err := s.repo.FindByID(ctx, tripID)
	if err != nil {
		return nil, err
	}
	if trip.Status != model.StatusCompleted {
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

	rating, err := s.repo.CreateRating(ctx, tripID, raterID, raterRole, rateeID, rateeRole, req.Score, req.Comment)
	if err != nil {
		return nil, err
	}

	ev := kafka.RatingSubmittedEvent{
		TripID:    tripID,
		RaterID:   raterID,
		RaterRole: raterRole,
		RateeID:   rateeID,
		RateeRole: rateeRole,
		Score:     req.Score,
		Comment:   req.Comment,
	}
	if err := s.kafka.Publish(ctx, kafka.TopicRatingSubmitted, tripID, ev); err != nil {
		log.Printf("[trip-service] failed to publish rating.submitted: %v", err)
	}

	return rating, nil
}

// PushLocation validates the driver is assigned to the trip and updates Redis GEO.
func (s *tripService) PushLocation(ctx context.Context, tripID, driverID string, lat, lng float64) error {
	trip, err := s.repo.FindByID(ctx, tripID)
	if err != nil {
		return errors.New("trip not found")
	}
	if trip.Status != model.StatusDriverAssigned && trip.Status != model.StatusStarted {
		return errors.New("location updates only allowed during active trips")
	}
	if trip.DriverID == nil || *trip.DriverID != driverID {
		return errors.New("you are not the driver on this trip")
	}
	return s.redis.SetDriverLocation(ctx, driverID, lat, lng)
}

func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLng := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func calcFare(km float64) float64 {
	return 50.0 + km*12.0
}

func calcDurationMin(km float64) float64 {
	return (km / 25.0) * 60.0
}
