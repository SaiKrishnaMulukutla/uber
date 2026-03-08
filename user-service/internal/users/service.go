package users

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
)

// Service contains user business logic.
type Service struct {
	db *pgxpool.Pool
}

// NewService creates a user service backed by the given pool.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Register creates a new rider account and returns a JWT.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (*AuthResponse, error) {
	var exists bool
	if err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)", req.Email).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("email already exists")
	}
	if err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE phone=$1)", req.Phone).Scan(&exists); err != nil {
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
	_, err = s.db.Exec(ctx,
		`INSERT INTO users (id,name,email,phone,password_hash,rating,rating_count) VALUES ($1,$2,$3,$4,$5,5.0,0)`,
		id, req.Name, req.Email, req.Phone, string(hash))
	if err != nil {
		return nil, err
	}

	pair, err := jwt.GenerateTokenPair(id, req.Email, "rider")
	if err != nil {
		return nil, err
	}

	return &AuthResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		User:         &User{ID: id, Name: req.Name, Email: req.Email, Phone: req.Phone, Rating: 5.0, RatingCount: 0},
	}, nil
}

// Login authenticates a user and returns a JWT.
func (s *Service) Login(ctx context.Context, req LoginRequest) (*AuthResponse, error) {
	var u User
	var hash string
	err := s.db.QueryRow(ctx,
		`SELECT id,name,email,phone,password_hash,rating,rating_count,created_at FROM users WHERE email=$1`,
		req.Email).Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &hash, &u.Rating, &u.RatingCount, &u.CreatedAt)
	if err != nil {
		return nil, errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return nil, errors.New("invalid credentials")
	}

	pair, err := jwt.GenerateTokenPair(u.ID, u.Email, "rider")
	if err != nil {
		return nil, err
	}
	return &AuthResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, User: &u}, nil
}

// Refresh validates a refresh token and returns a new token pair.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*RefreshResponse, error) {
	claims, err := jwt.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, errors.New("invalid refresh token")
	}
	if claims.Role != "rider" {
		return nil, errors.New("invalid token role")
	}
	pair, err := jwt.GenerateTokenPair(claims.UserID, claims.Email, claims.Role)
	if err != nil {
		return nil, err
	}
	return &RefreshResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken}, nil
}

// GetByID fetches a single user by primary key.
func (s *Service) GetByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.QueryRow(ctx,
		`SELECT id,name,email,phone,rating,rating_count,created_at FROM users WHERE id=$1`, id).
		Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &u.Rating, &u.RatingCount, &u.CreatedAt)
	if err != nil {
		return nil, errors.New("user not found")
	}
	return &u, nil
}

// StartRatingConsumer subscribes to rating.submitted events and updates rider ratings.
func (s *Service) StartRatingConsumer(ctx context.Context, k *kafka.Client) {
	k.Subscribe(ctx, kafka.TopicRatingSubmitted, "user-rating-update", func(data []byte) error {
		var ev events.RatingSubmittedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if ev.RateeRole != "rider" {
			return nil
		}
		log.Printf("[users] rating.submitted: rider=%s score=%d", ev.RateeID, ev.Score)
		_, err := s.db.Exec(ctx,
			`UPDATE users SET
			   rating_count = rating_count + 1,
			   rating = rating + ($1::float - rating) / (rating_count + 1)
			 WHERE id = $2`, ev.Score, ev.RateeID)
		return err
	})
}
