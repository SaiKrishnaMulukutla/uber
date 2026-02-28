package drivers

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"ride-service/pkg/jwt"
	rredis "ride-service/pkg/redis"
)

// Service contains driver business logic.
type Service struct {
	db    *pgxpool.Pool
	redis *rredis.Client
}

// NewService creates a driver service.
func NewService(db *pgxpool.Pool, redis *rredis.Client) *Service {
	return &Service{db: db, redis: redis}
}

// Register creates a new driver account and returns a JWT.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (*AuthResponse, error) {
	var exists bool
	_ = s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM drivers WHERE email=$1)", req.Email).Scan(&exists)
	if exists {
		return nil, errors.New("email already exists")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()
	vt := req.VehicleType
	if vt == "" {
		vt = "sedan"
	}

	_, err = s.db.Exec(ctx,
		`INSERT INTO drivers (id,name,email,phone,password_hash,vehicle_type,license_plate,status,rating)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'available',5.0)`,
		id, req.Name, req.Email, req.Phone, string(hash), vt, req.LicensePlate)
	if err != nil {
		return nil, err
	}

	token, err := jwt.Generate(id, req.Email, "driver")
	if err != nil {
		return nil, err
	}

	return &AuthResponse{
		Token: token,
		Driver: &Driver{
			ID: id, Name: req.Name, Email: req.Email, Phone: req.Phone,
			VehicleType: vt, LicensePlate: req.LicensePlate,
			Status: "available", Rating: 5.0,
		},
	}, nil
}

// Login authenticates a driver and returns a JWT.
func (s *Service) Login(ctx context.Context, req LoginRequest) (*AuthResponse, error) {
	var d Driver
	var hash string
	err := s.db.QueryRow(ctx,
		`SELECT id,name,email,phone,password_hash,vehicle_type,license_plate,status,rating,created_at
		 FROM drivers WHERE email=$1`, req.Email).
		Scan(&d.ID, &d.Name, &d.Email, &d.Phone, &hash,
			&d.VehicleType, &d.LicensePlate, &d.Status, &d.Rating, &d.CreatedAt)
	if err != nil {
		return nil, errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return nil, errors.New("invalid credentials")
	}

	token, err := jwt.Generate(d.ID, d.Email, "driver")
	if err != nil {
		return nil, err
	}
	return &AuthResponse{Token: token, Driver: &d}, nil
}

// GetByID fetches a driver by primary key.
func (s *Service) GetByID(ctx context.Context, id string) (*Driver, error) {
	var d Driver
	err := s.db.QueryRow(ctx,
		`SELECT id,name,email,phone,vehicle_type,license_plate,status,rating,created_at
		 FROM drivers WHERE id=$1`, id).
		Scan(&d.ID, &d.Name, &d.Email, &d.Phone,
			&d.VehicleType, &d.LicensePlate, &d.Status, &d.Rating, &d.CreatedAt)
	if err != nil {
		return nil, errors.New("driver not found")
	}
	return &d, nil
}

// UpdateLocation stores the driver's current position in Redis.
func (s *Service) UpdateLocation(ctx context.Context, driverID string, lat, lng float64) error {
	return s.redis.SetDriverLocation(ctx, driverID, lat, lng)
}

// GetNearby returns driver IDs within radiusKm of the given point.
func (s *Service) GetNearby(ctx context.Context, lat, lng, radiusKm float64) ([]string, error) {
	return s.redis.GetNearbyDrivers(ctx, lat, lng, radiusKm, 10)
}
