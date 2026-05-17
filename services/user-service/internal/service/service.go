package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"uber/shared/pkg/jwt"
	"uber/shared/pkg/mailer"
	"uber/user-service/internal/model"
	"uber/user-service/internal/repositories"
)

const pendingRegTTL = 10 * time.Minute

// OTPClient abstracts OTP send/verify.
type OTPClient interface {
	SendOTP(ctx context.Context, email string) error
	VerifyOTP(ctx context.Context, email, otp string) error
}

type UserService interface {
	Register(ctx context.Context, req model.RegisterRequest) error
	VerifyRegister(ctx context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error)
	Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error)
	Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error)
	GetByID(ctx context.Context, id string) (*model.User, error)
	Update(ctx context.Context, id string, req model.UpdateRequest) (*model.User, error)
}

type pendingRegistration struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
	Hash  string `json:"hash"`
}

type userService struct {
	repo   repositories.UserRepository
	otp    OTPClient
	rdb    goredis.UniversalClient
	mailer mailer.Mailer
}

func NewService(repo repositories.UserRepository, otp OTPClient, rdb goredis.UniversalClient, m mailer.Mailer) UserService {
	return &userService{repo: repo, otp: otp, rdb: rdb, mailer: m}
}

// Register validates input, stores pending data in Redis, and sends an OTP.
// The account is NOT created until VerifyRegister is called.
func (s *userService) Register(ctx context.Context, req model.RegisterRequest) error {
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

	pending := pendingRegistration{Name: req.Name, Email: req.Email, Phone: req.Phone, Hash: string(hash)}
	data, err := json.Marshal(pending)
	if err != nil {
		return fmt.Errorf("register: marshal: %w", err)
	}
	if err := s.rdb.Set(ctx, "pending_reg:"+req.Email, data, pendingRegTTL).Err(); err != nil {
		return fmt.Errorf("register: store: %w", err)
	}

	return s.otp.SendOTP(ctx, req.Email)
}

// VerifyRegister confirms the OTP, creates the account, and returns a JWT.
func (s *userService) VerifyRegister(ctx context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
	if err := s.otp.VerifyOTP(ctx, req.Email, req.OTP); err != nil {
		return nil, err
	}

	data, err := s.rdb.GetDel(ctx, "pending_reg:"+req.Email).Bytes()
	if err != nil {
		return nil, errors.New("registration session expired — please register again")
	}
	var pending pendingRegistration
	if err := json.Unmarshal(data, &pending); err != nil {
		return nil, errors.New("invalid registration data")
	}

	id := uuid.New().String()
	if err := s.repo.Create(ctx, id, pending.Name, pending.Email, pending.Phone, pending.Hash); err != nil {
		return nil, err
	}

	pair, err := jwt.GenerateTokenPair(id, pending.Email, pending.Phone, "rider")
	if err != nil {
		return nil, err
	}

	if s.mailer != nil {
		if err := s.mailer.Send(pending.Email, "Welcome to RideGo!", mailer.WelcomeRider(pending.Name)); err != nil {
			log.Printf("[user-service] failed to send welcome email to %s: %v", pending.Email, err)
		}
	}

	return &model.AuthResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		User:         &model.User{ID: id, Name: pending.Name, Email: pending.Email, Phone: pending.Phone, Rating: 5.0},
	}, nil
}

// Login validates credentials and returns a JWT directly.
func (s *userService) Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
	u, hash, err := s.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		return nil, errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return nil, errors.New("invalid credentials")
	}
	pair, err := jwt.GenerateTokenPair(u.ID, u.Email, u.Phone, "rider")
	if err != nil {
		return nil, err
	}
	return &model.AuthResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken, User: u}, nil
}

func (s *userService) Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error) {
	claims, err := jwt.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, errors.New("invalid refresh token")
	}
	if claims.Role != "rider" {
		return nil, errors.New("invalid token role")
	}
	pair, err := jwt.GenerateTokenPair(claims.UserID, claims.Email, claims.Phone, claims.Role)
	if err != nil {
		return nil, err
	}
	return &model.RefreshResponse{AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken}, nil
}

func (s *userService) GetByID(ctx context.Context, id string) (*model.User, error) {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, errors.New("user not found")
	}
	return u, nil
}

func (s *userService) Update(ctx context.Context, id string, req model.UpdateRequest) (*model.User, error) {
	return s.repo.Update(ctx, id, req.Name, req.Phone)
}

