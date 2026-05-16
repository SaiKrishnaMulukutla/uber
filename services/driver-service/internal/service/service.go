package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"uber/driver-service/internal/model"
	"uber/driver-service/internal/repositories"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
	"uber/shared/pkg/mailer"
	rredis "uber/shared/pkg/redis"
)

// OTPClient abstracts calls to the otp-service.
type OTPClient interface {
	SendOTP(ctx context.Context, email string) error
	VerifyOTP(ctx context.Context, email, otp string) error
}

// DriverService defines driver business operations.
type DriverService interface {
	Register(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error)
	Login(ctx context.Context, req model.LoginRequest) error
	VerifyLogin(ctx context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error)
	Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error)
	GetByID(ctx context.Context, id string) (*model.Driver, error)
	UpdateLocation(ctx context.Context, driverID string, lat, lng float64) error
	UpdateStatus(ctx context.Context, driverID, status string) (*model.Driver, error)
	GetNearby(ctx context.Context, lat, lng, radiusKm float64) ([]string, error)
	RespondToOffer(ctx context.Context, driverID, tripID string, accept bool) error
}

type driverService struct {
	repo   repositories.DriverRepository
	redis  *rredis.Client
	kafka  *kafka.Client
	otp    OTPClient
	mailer mailer.Mailer
}

// New returns a DriverService backed by the given repository, Redis client, OTP client, mailer, and Kafka client.
func New(repo repositories.DriverRepository, redis *rredis.Client, otp OTPClient, m mailer.Mailer, k *kafka.Client) DriverService {
	return &driverService{repo: repo, redis: redis, otp: otp, mailer: m, kafka: k}
}

func (s *driverService) Register(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error) {
	if exists, err := s.repo.EmailExists(ctx, req.Email); err != nil {
		return nil, err
	} else if exists {
		return nil, errors.New("registration failed")
	}
	if exists, err := s.repo.PhoneExists(ctx, req.Phone); err != nil {
		return nil, err
	} else if exists {
		return nil, errors.New("registration failed")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	vt := req.VehicleType
	if vt == "" {
		vt = model.DefaultVehicleType
	}

	d := &model.Driver{
		ID:           uuid.New().String(),
		Name:         req.Name,
		Email:        req.Email,
		Phone:        req.Phone,
		VehicleType:  vt,
		LicensePlate: req.LicensePlate,
		Status:       model.StatusAvailable,
		Rating:       model.DefaultRating,
		RatingCount:  model.DefaultRatingCount,
	}

	if err := s.repo.Create(ctx, d, string(hash)); err != nil {
		return nil, err
	}

	if err := s.redis.SetDriverType(ctx, d.ID, d.VehicleType); err != nil {
		log.Printf("[driver-service] warn: failed to cache vehicle type for driver %s: %v", d.ID, err)
	}

	pair, err := jwt.GenerateTokenPair(d.ID, d.Email, "", model.RoleDriver)
	if err != nil {
		return nil, err
	}

	if s.mailer != nil {
		if err := s.mailer.Send(d.Email, "Welcome to RideGo!", mailer.WelcomeDriver(d.Name)); err != nil {
			log.Printf("[driver-service] failed to send welcome email to %s: %v", d.Email, err)
		}
	}

	return &model.AuthResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		Driver:       d,
	}, nil
}

// Login validates credentials and triggers an OTP send. No JWT is issued here.
func (s *driverService) Login(ctx context.Context, req model.LoginRequest) error {
	d, hash, err := s.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		return errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return errors.New("invalid credentials")
	}
	return s.otp.SendOTP(ctx, d.Email)
}

// VerifyLogin confirms the OTP and issues a JWT on success.
func (s *driverService) VerifyLogin(ctx context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error) {
	if err := s.otp.VerifyOTP(ctx, req.Email, req.OTP); err != nil {
		return nil, err
	}
	d, _, err := s.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		return nil, errors.New("driver not found")
	}
	pair, err := jwt.GenerateTokenPair(d.ID, d.Email, "", model.RoleDriver)
	if err != nil {
		return nil, err
	}
	return &model.AuthResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, Driver: d}, nil
}

func (s *driverService) Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error) {
	claims, err := jwt.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, errors.New("invalid refresh token")
	}
	if claims.Role != model.RoleDriver {
		return nil, errors.New("invalid token role")
	}
	pair, err := jwt.GenerateTokenPair(claims.UserID, claims.Email, "", claims.Role)
	if err != nil {
		return nil, err
	}
	return &model.RefreshResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken}, nil
}

func (s *driverService) GetByID(ctx context.Context, id string) (*model.Driver, error) {
	return s.repo.FindByID(ctx, id)
}

// UpdateLocation stores the driver's position in Redis (GEO set + backup key).
func (s *driverService) UpdateLocation(ctx context.Context, driverID string, lat, lng float64) error {
	if err := s.redis.SaveDriverLocation(ctx, driverID, lat, lng); err != nil {
		log.Printf("[driver-service] failed to save location backup for %s: %v", driverID, err)
	}
	return s.redis.SetDriverLocation(ctx, driverID, lat, lng)
}

// UpdateStatus persists the status to Postgres and syncs Redis GEO accordingly.
func (s *driverService) UpdateStatus(ctx context.Context, driverID, status string) (*model.Driver, error) {
	if err := s.repo.UpdateStatus(ctx, driverID, status); err != nil {
		return nil, err
	}
	driver, err := s.repo.FindByID(ctx, driverID)
	if err != nil {
		return nil, err
	}
	switch status {
	case model.StatusAvailable:
		if lat, lng, locErr := s.redis.GetDriverLocation(ctx, driverID); locErr == nil {
			_ = s.redis.SetDriverLocation(ctx, driverID, lat, lng)
		}
		if cacheErr := s.redis.SetDriverType(ctx, driverID, driver.VehicleType); cacheErr != nil {
			log.Printf("[driver-service] warn: failed to cache vehicle type for driver %s: %v", driverID, cacheErr)
		}
	default:
		_ = s.redis.RemoveDriverLocation(ctx, driverID)
	}
	return driver, nil
}

// GetNearby returns driver IDs within radiusKm of the given point.
func (s *driverService) GetNearby(ctx context.Context, lat, lng, radiusKm float64) ([]string, error) {
	return s.redis.GetNearbyDrivers(ctx, lat, lng, radiusKm, 10)
}

// RespondToOffer accepts or rejects a pending ride offer from the matching service.
// On accept: publishes driver.assigned so the trip transitions to DRIVER_ASSIGNED.
// On reject: unlocks the driver, restores them to the GEO pool, and re-queues the
// trip so matching can try the next available driver.
func (s *driverService) RespondToOffer(ctx context.Context, driverID, tripID string, accept bool) error {
	stored, err := s.redis.GetOffer(ctx, tripID)
	if err != nil || stored != driverID {
		return errors.New("no pending offer for this trip")
	}
	if err := s.redis.DeleteOffer(ctx, tripID); err != nil {
		return fmt.Errorf("failed to consume offer: %w", err)
	}

	if accept {
		riderID := ""
		if eventData, evErr := s.redis.GetOfferEvent(ctx, tripID); evErr == nil {
			var origEv kafka.RideRequestedEvent
			if jsonErr := json.Unmarshal(eventData, &origEv); jsonErr == nil {
				riderID = origEv.RiderID
			}
		}
		ev := kafka.DriverAssignedEvent{TripID: tripID, DriverID: driverID, RiderID: riderID}
		if err := s.kafka.Publish(ctx, kafka.TopicDriverAssigned, tripID, ev); err != nil {
			return fmt.Errorf("failed to publish driver.assigned: %w", err)
		}
		return nil
	}

	// Rejected: restore driver to GEO pool and re-queue the trip.
	_ = s.redis.UnlockDriver(ctx, driverID)
	if lat, lng, locErr := s.redis.GetDriverLocation(ctx, driverID); locErr == nil {
		_ = s.redis.SetDriverLocation(ctx, driverID, lat, lng)
	}

	eventData, err := s.redis.GetOfferEvent(ctx, tripID)
	if err != nil {
		// Non-fatal: the timeout goroutine in matching-service will re-queue after offerTTL.
		log.Printf("[driver-service] no offer event for trip %s, matching timeout will handle re-queue: %v", tripID, err)
		return nil
	}
	var origEv kafka.RideRequestedEvent
	if err := json.Unmarshal(eventData, &origEv); err != nil {
		log.Printf("[driver-service] could not unmarshal offer event for trip %s: %v", tripID, err)
		return nil
	}
	origEv.SkipDriverIDs = append(origEv.SkipDriverIDs, driverID)
	if err := s.kafka.Publish(ctx, kafka.TopicRideRequested, tripID, origEv); err != nil {
		return fmt.Errorf("failed to re-queue ride.requested: %w", err)
	}
	return nil
}
