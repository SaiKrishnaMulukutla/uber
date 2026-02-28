package drivers

import "time"

// Driver represents a driver account.
type Driver struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	Phone        string    `json:"phone"`
	PasswordHash string    `json:"-"`
	VehicleType  string    `json:"vehicle_type"`
	LicensePlate string    `json:"license_plate"`
	Status       string    `json:"status"` // available | busy | offline
	Rating       float64   `json:"rating"`
	CreatedAt    time.Time `json:"created_at"`
}

// RegisterRequest is the body for POST /drivers/register.
type RegisterRequest struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	Password     string `json:"password"`
	VehicleType  string `json:"vehicle_type"`
	LicensePlate string `json:"license_plate"`
}

// LoginRequest is the body for POST /drivers/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LocationUpdate is the body for PATCH /drivers/:id/location.
type LocationUpdate struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// AuthResponse is returned on register / login.
type AuthResponse struct {
	Token  string  `json:"token"`
	Driver *Driver `json:"driver,omitempty"`
}
