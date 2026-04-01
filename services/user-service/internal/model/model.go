package model

import "time"

type User struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Phone        string    `json:"phone"`
	PasswordHash string    `json:"-"`
	Rating       float64   `json:"rating"`
	RatingCount  int       `json:"rating_count"`
	CreatedAt    time.Time `json:"created_at"`
}

type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type VerifyLoginRequest struct {
	Email string `json:"email"`
	OTP   string `json:"otp"`
}

type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         *User  `json:"user,omitempty"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}
