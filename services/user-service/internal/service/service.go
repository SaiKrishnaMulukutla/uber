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
		if err := s.mailer.Send(req.Email, "Welcome to RideGo!", welcomeEmailBody(req.Name)); err != nil {
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
<h2 style="margin:0 0 12px;font-size:22px;font-weight:700;color:#0F172A;">Welcome aboard, %s!</h2>
<p style="margin:0 0 24px;font-size:15px;color:#475569;line-height:1.7;">
  Your RideGo account is ready. You can now book rides and get where you need to go — quickly and reliably.
</p>
<table cellpadding="0" cellspacing="0" border="0" width="100%%" style="margin-bottom:24px;">
  <tr>
    <td style="background:#F8FAFC;border:1px solid #E2E8F0;border-left:4px solid #0EA5E9;border-radius:4px;padding:16px 20px;">
      <p style="margin:0 0 4px;font-size:11px;font-weight:600;color:#94A3B8;letter-spacing:1px;text-transform:uppercase;">Getting started</p>
      <p style="margin:0;font-size:14px;color:#334155;line-height:1.6;">Open the app &rarr; set your pickup &rarr; confirm your ride.</p>
    </td>
  </tr>
</table>
<p style="margin:0;font-size:13px;color:#94A3B8;line-height:1.6;">
  If you didn't create this account, please contact our support team immediately.
</p>`, name)
	return buildEmailLayout(content)
}

func buildEmailLayout(content string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8"/>
  <meta name="viewport" content="width=device-width,initial-scale=1.0"/>
  <title>RideGo</title>
</head>
<body style="margin:0;padding:0;background-color:#F1F5F9;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#F1F5F9;padding:48px 0;">
    <tr><td align="center">
      <table width="560" cellpadding="0" cellspacing="0" border="0"
             style="background-color:#ffffff;border-radius:8px;overflow:hidden;box-shadow:0 4px 24px rgba(0,0,0,0.07);">
        <tr>
          <td style="background-color:#1A1A2E;padding:32px 48px;">
            <span style="color:#ffffff;font-size:22px;font-weight:700;letter-spacing:1px;">RIDEGO</span>
            <p style="margin:6px 0 0;color:rgba(255,255,255,0.45);font-size:11px;letter-spacing:2px;text-transform:uppercase;">On-demand rides</p>
          </td>
        </tr>
        <tr><td style="padding:48px 48px 36px;">%s</td></tr>
        <tr><td style="padding:0 48px;"><hr style="border:none;border-top:1px solid #E2E8F0;margin:0;"/></td></tr>
        <tr><td style="padding:28px 48px 36px;">
          <p style="margin:0 0 4px;font-size:12px;color:#94A3B8;line-height:1.7;">
            This is an automated message from RideGo. Please do not reply to this email.
          </p>
          <p style="margin:12px 0 0;font-size:12px;color:#CBD5E1;line-height:1.6;text-align:center;">
            Created with &#10084;&#65039; by Mulukutla Sai Krishna
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`, content)
}
