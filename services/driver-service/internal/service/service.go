package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"uber/driver-service/internal/model"
	"uber/driver-service/internal/repositories"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
	"uber/shared/pkg/mailer"
	rredis "uber/shared/pkg/redis"
)

const pendingRegTTL = 10 * time.Minute

type pendingRegistration struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	Hash         string `json:"hash"`
	VehicleType  string `json:"vehicle_type"`
	LicensePlate string `json:"license_plate"`
}

// OTPClient abstracts calls to the otp-service.
type OTPClient interface {
	SendOTP(ctx context.Context, email string) error
	VerifyOTP(ctx context.Context, email, otp string) error
}

// DriverService defines driver business operations.
type DriverService interface {
	Register(ctx context.Context, req model.RegisterRequest) error
	VerifyRegister(ctx context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error)
	Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error)
	Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error)
	GetByID(ctx context.Context, id string) (*model.Driver, error)
	UpdateLocation(ctx context.Context, driverID string, lat, lng float64) error
	UpdateStatus(ctx context.Context, driverID, status string) (*model.Driver, error)
	GetNearby(ctx context.Context, lat, lng, radiusKm float64) ([]string, error)
	RespondToOffer(ctx context.Context, driverID, tripID string, accept bool) error
	Update(ctx context.Context, id string, req model.UpdateRequest) (*model.Driver, error)
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

// Register validates input, stores pending data in Redis, and sends an OTP.
func (s *driverService) Register(ctx context.Context, req model.RegisterRequest) error {
	if exists, err := s.repo.EmailExists(ctx, req.Email); err != nil {
		return err
	} else if exists {
		return errors.New("email already registered")
	}
	if exists, err := s.repo.PhoneExists(ctx, req.Phone); err != nil {
		return err
	} else if exists {
		return errors.New("phone already registered")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	vt := req.VehicleType
	if vt == "" {
		vt = model.DefaultVehicleType
	}

	pending := pendingRegistration{
		Name: req.Name, Email: req.Email, Phone: req.Phone,
		Hash: string(hash), VehicleType: vt, LicensePlate: req.LicensePlate,
	}
	data, err := json.Marshal(pending)
	if err != nil {
		return fmt.Errorf("register: marshal: %w", err)
	}
	if err := s.redis.RDB().Set(ctx, "pending_reg:"+req.Email, data, pendingRegTTL).Err(); err != nil {
		return fmt.Errorf("register: store: %w", err)
	}
	return s.otp.SendOTP(ctx, req.Email)
}

// VerifyRegister confirms the OTP, creates the account, and returns a JWT.
func (s *driverService) VerifyRegister(ctx context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
	if err := s.otp.VerifyOTP(ctx, req.Email, req.OTP); err != nil {
		return nil, err
	}

	data, err := s.redis.RDB().GetDel(ctx, "pending_reg:"+req.Email).Bytes()
	if err != nil {
		return nil, errors.New("registration session expired — please register again")
	}
	var pending pendingRegistration
	if err := json.Unmarshal(data, &pending); err != nil {
		return nil, errors.New("invalid registration data")
	}

	d := &model.Driver{
		ID:           uuid.New().String(),
		Name:         pending.Name,
		Email:        pending.Email,
		Phone:        pending.Phone,
		VehicleType:  pending.VehicleType,
		LicensePlate: pending.LicensePlate,
		Status:       model.StatusAvailable,
		Rating:       model.DefaultRating,
		RatingCount:  model.DefaultRatingCount,
	}
	if err := s.repo.Create(ctx, d, pending.Hash); err != nil {
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
	return &model.AuthResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, Driver: d}, nil
}

// Login validates credentials and returns a JWT directly.
func (s *driverService) Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
	d, hash, err := s.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		return nil, errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return nil, errors.New("invalid credentials")
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

func (s *driverService) Update(ctx context.Context, id string, req model.UpdateRequest) (*model.Driver, error) {
	d, err := s.repo.Update(ctx, id, req.Name, req.Phone, req.VehicleType, req.LicensePlate)
	if err != nil {
		return nil, err
	}
	// If vehicle type changed, sync the Redis cache so matching uses the new tier.
	if req.VehicleType != "" {
		if cacheErr := s.redis.SetDriverType(ctx, id, d.VehicleType); cacheErr != nil {
			log.Printf("[driver-service] warn: failed to update cached vehicle type for driver %s: %v", id, cacheErr)
		}
	}
	return d, nil
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
