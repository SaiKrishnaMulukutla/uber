package validation

import (
	"regexp"
	"strings"
)

var (
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	phoneRegex = regexp.MustCompile(`^\+?[1-9]\d{1,14}$`)
)

func ValidateEmail(email string) bool {
	email = strings.TrimSpace(email)
	return email != "" && emailRegex.MatchString(email) && len(email) <= 200
}

func ValidatePhone(phone string) bool {
	phone = strings.TrimSpace(phone)
	return phone != "" && phoneRegex.MatchString(phone) && len(phone) <= 50
}

func ValidateName(name string) bool {
	name = strings.TrimSpace(name)
	return len(name) >= 2 && len(name) <= 200
}

func ValidatePassword(password string) bool {
	return len(password) >= 6 && len(password) <= 100
}

func ValidateCoordinates(lat, lng float64) bool {
	return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}
