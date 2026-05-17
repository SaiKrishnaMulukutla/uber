package model

import "time"

const (
	StatusAvailable    = "available"
	StatusBusy         = "busy"
	StatusOffline      = "offline"
	DefaultVehicleType = "x"
	DefaultRating      = 5.0
	DefaultRatingCount = 0
	RoleDriver         = "driver"
)

type Driver struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Phone        string    `json:"phone"`
	PasswordHash string    `json:"-"`
	VehicleType  string    `json:"vehicle_type"`
	LicensePlate string    `json:"license_plate"`
	Status       string    `json:"status"`
	Rating       float64   `json:"rating"`
	RatingCount  int       `json:"rating_count"`
	CreatedAt    time.Time `json:"created_at"`
}

type RegisterRequest struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	Password     string `json:"password"`
	VehicleType  string `json:"vehicle_type"`
	LicensePlate string `json:"license_plate"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type VerifyRegisterRequest struct {
	Email string `json:"email"`
	OTP   string `json:"otp"`
}

type AuthResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	Driver       *Driver `json:"driver,omitempty"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type RefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type LocationUpdate struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type StatusUpdate struct {
	Status string `json:"status"`
}

type UpdateRequest struct {
	Name         string `json:"name"`
	Phone        string `json:"phone"`
	VehicleType  string `json:"vehicle_type"`
	LicensePlate string `json:"license_plate"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

type ResetPasswordRequest struct {
	Email       string `json:"email"`
	OTP         string `json:"otp"`
	NewPassword string `json:"new_password"`
}
