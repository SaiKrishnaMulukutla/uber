package service

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"uber/shared/pkg/jwt"
	"uber/shared/pkg/mailer"
	"uber/user-service/internal/model"
	"uber/user-service/internal/repositories"
)

// OTPClient abstracts calls to the otp-service.
type OTPClient interface {
	SendOTP(ctx context.Context, email string) error
	VerifyOTP(ctx context.Context, email, otp string) error
}

type UserService interface {
	Register(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error)
	Login(ctx context.Context, req model.LoginRequest) error
	VerifyLogin(ctx context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error)
	Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error)
	GetByID(ctx context.Context, id string) (*model.User, error)
}

type userService struct {
	repo   repositories.UserRepository
	otp    OTPClient
	mailer mailer.Mailer
}

func NewService(repo repositories.UserRepository, otp OTPClient, m mailer.Mailer) UserService {
	return &userService{repo: repo, otp: otp, mailer: m}
}

func (s *userService) Register(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error) {
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

	id := uuid.New().String()
	if err := s.repo.Create(ctx, id, req.Name, req.Email, req.Phone, string(hash)); err != nil {
		return nil, err
	}

	pair, err := jwt.GenerateTokenPair(id, req.Email, req.Phone, "rider")
	if err != nil {
		return nil, err
	}

	if s.mailer != nil {
		if err := s.mailer.Send(req.Email, "Welcome to Uber!", welcomeEmailBody(req.Name)); err != nil {
			log.Printf("[user-service] failed to send welcome email to %s: %v", req.Email, err)
		}
	}

	return &model.AuthResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		User: &model.User{
			ID: id, Name: req.Name, Email: req.Email, Phone: req.Phone,
			Rating: 5.0, RatingCount: 0,
		},
	}, nil
}

// Login validates credentials and triggers an OTP send. No JWT is issued here.
func (s *userService) Login(ctx context.Context, req model.LoginRequest) error {
	u, hash, err := s.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		return errors.New("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		return errors.New("invalid credentials")
	}
	return s.otp.SendOTP(ctx, u.Email)
}

// VerifyLogin confirms the OTP and issues a JWT on success.
func (s *userService) VerifyLogin(ctx context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error) {
	if err := s.otp.VerifyOTP(ctx, req.Email, req.OTP); err != nil {
		return nil, err
	}
	u, _, err := s.repo.FindByEmail(ctx, req.Email)
	if err != nil {
		return nil, errors.New("user not found")
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

func welcomeEmailBody(name string) string {
	content := fmt.Sprintf(`
<p style="margin:0 0 8px;font-size:22px;font-weight:700;color:#000000;">Welcome to Uber, %s!</p>
<p style="margin:0 0 20px;font-size:15px;color:#545454;line-height:1.6;">
  Your account has been created successfully. Start booking rides right away.
</p>
<p style="margin:0;font-size:13px;color:#888888;line-height:1.6;">
  If you didn't create this account, please contact support immediately.
</p>`, name)
	return buildEmailLayout(content)
}

func buildEmailLayout(content string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"/><title>Uber</title></head>
<body style="margin:0;padding:0;background-color:#f6f6f6;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f6f6f6;padding:40px 0;">
    <tr><td align="center">
      <table width="520" cellpadding="0" cellspacing="0" border="0"
             style="background-color:#ffffff;border-radius:4px;overflow:hidden;box-shadow:0 2px 8px rgba(0,0,0,0.08);">
        <tr><td style="background-color:#000000;padding:28px 40px;">
          <span style="color:#ffffff;font-size:26px;font-weight:700;letter-spacing:-0.5px;">Uber</span>
        </td></tr>
        <tr><td style="padding:40px 40px 28px;">%s</td></tr>
        <tr><td style="padding:0 40px;"><hr style="border:none;border-top:1px solid #eeeeee;margin:0;"/></td></tr>
        <tr><td style="padding:24px 40px 32px;">
          <p style="margin:0 0 4px;font-size:12px;color:#aaaaaa;line-height:1.6;">
            This is an automated message from Uber. Please do not reply to this email.
          </p>
          <p style="margin:8px 0 0;font-size:12px;color:#555555;line-height:1.6;text-align:center;">
            Created with &#10084;&#65039; by Mulukutla Sai Krishna
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, content)
}
