package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"uber/shared/events"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
	rredis "uber/shared/pkg/redis"
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
	if err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM drivers WHERE email=$1)", req.Email).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("email already exists")
	}
	if err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM drivers WHERE phone=$1)", req.Phone).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("phone already exists")
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
		`INSERT INTO drivers (id,name,email,phone,password_hash,vehicle_type,license_plate,status,rating,rating_count)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'available',5.0,0)`,
		id, req.Name, req.Email, req.Phone, string(hash), vt, req.LicensePlate)
	if err != nil {
		return nil, err
	}

	pair, err := jwt.GenerateTokenPair(id, req.Email, "driver")
	if err != nil {
		return nil, err
	}

	return &AuthResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		Driver: &Driver{
			ID: id, Name: req.Name, Email: req.Email, Phone: req.Phone,
			VehicleType: vt, LicensePlate: req.LicensePlate,
			Status: "available", Rating: 5.0, RatingCount: 0,
		},
	}, nil
}

// Login authenticates a driver and returns a JWT.
func (s *Service) Login(ctx context.Context, req LoginRequest) (*AuthResponse, error) {
	var d Driver
	var hash string
	err := s.db.QueryRow(ctx,
		`SELECT id,name,email,phone,password_hash,vehicle_type,license_plate,status,rating,rating_count,created_at
		 FROM drivers WHERE email=$1`, req.Email).
		Scan(&d.ID, &d.Name, &d.Email, &d.Phone, &hash,
			&d.VehicleType, &d.LicensePlate, &d.Status, &d.Rating, &d.RatingCount, &d.CreatedAt)
	if err != nil {
		return nil, errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return nil, errors.New("invalid credentials")
	}

	pair, err := jwt.GenerateTokenPair(d.ID, d.Email, "driver")
	if err != nil {
		return nil, err
	}
	return &AuthResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, Driver: &d}, nil
}

// Refresh validates a refresh token and returns a new token pair.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*RefreshResponse, error) {
	claims, err := jwt.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, errors.New("invalid refresh token")
	}
	if claims.Role != "driver" {
		return nil, errors.New("invalid token role")
	}
	pair, err := jwt.GenerateTokenPair(claims.UserID, claims.Email, claims.Role)
	if err != nil {
		return nil, err
	}
	return &RefreshResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken}, nil
}

// GetByID fetches a driver by primary key.
func (s *Service) GetByID(ctx context.Context, id string) (*Driver, error) {
	var d Driver
	err := s.db.QueryRow(ctx,
		`SELECT id,name,email,phone,vehicle_type,license_plate,status,rating,rating_count,created_at
		 FROM drivers WHERE id=$1`, id).
		Scan(&d.ID, &d.Name, &d.Email, &d.Phone,
			&d.VehicleType, &d.LicensePlate, &d.Status, &d.Rating, &d.RatingCount, &d.CreatedAt)
	if err != nil {
		return nil, errors.New("driver not found")
	}
	return &d, nil
}

// UpdateLocation stores the driver's current position in Redis and saves a
// backup key so the position can be restored after assignment or cancellation.
func (s *Service) UpdateLocation(ctx context.Context, driverID string, lat, lng float64) error {
	if err := s.redis.SaveDriverLocation(ctx, driverID, lat, lng); err != nil {
		log.Printf("[drivers] failed to save location backup for %s: %v", driverID, err)
	}
	return s.redis.SetDriverLocation(ctx, driverID, lat, lng)
}

// UpdateStatus sets a driver's status in Postgres and syncs the Redis GEO pool.
func (s *Service) UpdateStatus(ctx context.Context, driverID, status string) (*Driver, error) {
	tag, err := s.db.Exec(ctx, `UPDATE drivers SET status=$1 WHERE id=$2`, status, driverID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, errors.New("driver not found")
	}
	switch status {
	case "available":
		if lat, lng, locErr := s.redis.GetDriverLocation(ctx, driverID); locErr == nil {
			_ = s.redis.SetDriverLocation(ctx, driverID, lat, lng)
		}
	default:
		_ = s.redis.RemoveDriverLocation(ctx, driverID)
	}
	return s.GetByID(ctx, driverID)
}

// GetNearby returns driver IDs within radiusKm of the given point.
func (s *Service) GetNearby(ctx context.Context, lat, lng, radiusKm float64) ([]string, error) {
	return s.redis.GetNearbyDrivers(ctx, lat, lng, radiusKm, 10)
}

// StartStatusConsumers subscribes to Kafka events that affect driver status.
// This replaces the cross-service DB writes that the trip-service used to do.
func (s *Service) StartStatusConsumers(ctx context.Context, k *kafka.Client) {
	k.Subscribe(ctx, kafka.TopicDriverAssigned, "driver-status-assigned", func(data []byte) error {
		var ev events.DriverAssignedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[drivers] driver.assigned: driver=%s trip=%s → status=busy", ev.DriverID, ev.TripID)
		_, err := s.db.Exec(ctx, `UPDATE drivers SET status='busy' WHERE id=$1`, ev.DriverID)
		return err
	})

	k.Subscribe(ctx, kafka.TopicTripCompleted, "driver-status-completed", func(data []byte) error {
		var ev events.TripCompletedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if ev.DriverID == "" {
			return nil
		}
		log.Printf("[drivers] trip.completed: driver=%s → status=available", ev.DriverID)
		_, err := s.db.Exec(ctx, `UPDATE drivers SET status='available' WHERE id=$1`, ev.DriverID)
		return err
	})

	k.Subscribe(ctx, kafka.TopicTripCancelled, "driver-status-cancelled", func(data []byte) error {
		var ev events.TripCancelledEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if ev.DriverID == "" {
			return nil
		}
		log.Printf("[drivers] trip.cancelled: driver=%s → status=available", ev.DriverID)
		_, err := s.db.Exec(ctx, `UPDATE drivers SET status='available' WHERE id=$1`, ev.DriverID)
		return err
	})

	k.Subscribe(ctx, kafka.TopicRatingSubmitted, "driver-rating-update", func(data []byte) error {
		var ev events.RatingSubmittedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if ev.RateeRole != "driver" {
			return nil
		}
		log.Printf("[drivers] rating.submitted: driver=%s score=%d", ev.RateeID, ev.Score)
		_, err := s.db.Exec(ctx,
			`UPDATE drivers SET
			   rating_count = rating_count + 1,
			   rating = rating + ($1::float - rating) / (rating_count + 1)
			 WHERE id = $2`, ev.Score, ev.RateeID)
		return err
	})
}
