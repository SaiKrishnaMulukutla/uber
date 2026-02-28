package users

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"ride-service/pkg/jwt"
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
	_ = s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)", req.Email).Scan(&exists)
	if exists {
		return nil, errors.New("email already exists")
	}
	_ = s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE phone=$1)", req.Phone).Scan(&exists)
	if exists {
		return nil, errors.New("phone already exists")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	id := uuid.New().String()
	_, err = s.db.Exec(ctx,
		`INSERT INTO users (id,name,email,phone,password_hash,rating) VALUES ($1,$2,$3,$4,$5,5.0)`,
		id, req.Name, req.Email, req.Phone, string(hash))
	if err != nil {
		return nil, err
	}

	token, err := jwt.Generate(id, req.Email, "rider")
	if err != nil {
		return nil, err
	}

	return &AuthResponse{
		Token: token,
		User:  &User{ID: id, Name: req.Name, Email: req.Email, Phone: req.Phone, Rating: 5.0},
	}, nil
}

// Login authenticates a user and returns a JWT.
func (s *Service) Login(ctx context.Context, req LoginRequest) (*AuthResponse, error) {
	var u User
	var hash string
	err := s.db.QueryRow(ctx,
		`SELECT id,name,email,phone,password_hash,rating,created_at FROM users WHERE email=$1`,
		req.Email).Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &hash, &u.Rating, &u.CreatedAt)
	if err != nil {
		return nil, errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return nil, errors.New("invalid credentials")
	}

	token, err := jwt.Generate(u.ID, u.Email, "rider")
	if err != nil {
		return nil, err
	}
	return &AuthResponse{Token: token, User: &u}, nil
}

// GetByID fetches a single user by primary key.
func (s *Service) GetByID(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.db.QueryRow(ctx,
		`SELECT id,name,email,phone,rating,created_at FROM users WHERE id=$1`, id).
		Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &u.Rating, &u.CreatedAt)
	if err != nil {
		return nil, errors.New("user not found")
	}
	return &u, nil
}
