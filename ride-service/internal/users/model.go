package users

import "time"

// User represents a rider account.
type User struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Phone        string    `json:"phone"`
	PasswordHash string    `json:"-"`
	Rating       float64   `json:"rating"`
	CreatedAt    time.Time `json:"created_at"`
}

// RegisterRequest is the body for POST /users/register.
type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

// LoginRequest is the body for POST /users/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// AuthResponse is returned on register / login.
type AuthResponse struct {
	Token string `json:"token"`
	User  *User  `json:"user,omitempty"`
}
