package service

import (
	"context"
	"errors"
	"log"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"uber/driver-service/internal/model"
	"uber/driver-service/internal/repositories"
	"uber/shared/pkg/jwt"
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
}

type driverService struct {
	repo  repositories.DriverRepository
	redis *rredis.Client
	otp   OTPClient
}

// New returns a DriverService backed by the given repository, Redis client, and OTP client.
func New(repo repositories.DriverRepository, redis *rredis.Client, otp OTPClient) DriverService {
	return &driverService{repo: repo, redis: redis, otp: otp}
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

	pair, err := jwt.GenerateTokenPair(d.ID, d.Email, model.RoleDriver)
	if err != nil {
		return nil, err
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
	pair, err := jwt.GenerateTokenPair(d.ID, d.Email, model.RoleDriver)
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
	pair, err := jwt.GenerateTokenPair(claims.UserID, claims.Email, claims.Role)
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
	switch status {
	case model.StatusAvailable:
		if lat, lng, err := s.redis.GetDriverLocation(ctx, driverID); err == nil {
			_ = s.redis.SetDriverLocation(ctx, driverID, lat, lng)
		}
	default:
		_ = s.redis.RemoveDriverLocation(ctx, driverID)
	}
	return s.repo.FindByID(ctx, driverID)
}

// GetNearby returns driver IDs within radiusKm of the given point.
func (s *driverService) GetNearby(ctx context.Context, lat, lng, radiusKm float64) ([]string, error) {
	return s.redis.GetNearbyDrivers(ctx, lat, lng, radiusKm, 10)
}
