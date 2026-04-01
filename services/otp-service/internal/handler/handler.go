package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"regexp"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"uber/otp-service/internal/service"
)

// sixDigits validates that the OTP is exactly 6 numeric digits.
var sixDigits = regexp.MustCompile(`^\d{6}$`)

// Handler holds a reference to the OTP service.
type Handler struct{ svc service.OTPService }

// New returns a Handler wired to the given OTPService.
func New(svc service.OTPService) *Handler { return &Handler{svc: svc} }

// SetupRouter mounts all OTP routes on a new Chi router.
func SetupRouter(h *Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Post("/send-otp", h.SendOTP)
	r.Post("/verify-otp", h.VerifyOTP)
	return r
}

// ---- Request / Response types ----

type sendOTPRequest struct {
	Email string `json:"email"`
}

type verifyOTPRequest struct {
	Email string `json:"email"`
	OTP   string `json:"otp"`
}

// ---- Helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// validEmail returns true if the address is RFC 5322-compliant.
func validEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// ---- Handlers ----

// SendOTP handles POST /send-otp
// Body: {"email": "user@example.com"}
// 200 → OTP sent | 400 → bad input | 429 → rate limited | 500 → internal error
func (h *Handler) SendOTP(w http.ResponseWriter, r *http.Request) {
	var req sendOTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
		return
	}
	if !validEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email address"})
		return
	}

	if err := h.svc.SendOTP(r.Context(), req.Email); err != nil {
		switch {
		case errors.Is(err, service.ErrRateLimitExceeded):
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send OTP — please try again"})
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "OTP sent to " + req.Email})
}

// VerifyOTP handles POST /verify-otp
// Body: {"email": "user@example.com", "otp": "123456"}
// 200 → verified | 400 → bad input/invalid OTP | 429 → attempts exceeded
func (h *Handler) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req verifyOTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.OTP == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and otp are required"})
		return
	}
	if !validEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email address"})
		return
	}
	if !sixDigits.MatchString(req.OTP) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "otp must be exactly 6 digits"})
		return
	}

	if err := h.svc.VerifyOTP(r.Context(), req.Email, req.OTP); err != nil {
		switch {
		case errors.Is(err, service.ErrMaxAttemptsExceeded):
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		case errors.Is(err, service.ErrOTPExpired),
			errors.Is(err, service.ErrInvalidOTP):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "verification failed — please try again"})
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "OTP verified successfully"})
}
