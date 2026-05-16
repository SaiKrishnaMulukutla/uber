package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"uber/driver-service/internal/model"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/otp"
	"uber/shared/pkg/validation"
)

var sixDigits = regexp.MustCompile(`^\d{6}$`)

// DriverServicer is the subset of service.DriverService the handler needs.
type DriverServicer interface {
	Register(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error)
	Login(ctx context.Context, req model.LoginRequest) error
	VerifyLogin(ctx context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error)
	Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error)
	GetByID(ctx context.Context, id string) (*model.Driver, error)
	UpdateLocation(ctx context.Context, driverID string, lat, lng float64) error
	UpdateStatus(ctx context.Context, driverID, status string) (*model.Driver, error)
	GetNearby(ctx context.Context, lat, lng, radiusKm float64) ([]string, error)
	RespondToOffer(ctx context.Context, driverID, tripID string, accept bool) error
	Update(ctx context.Context, id string, req model.UpdateRequest) (*model.Driver, error)
}

// Handler exposes driver HTTP endpoints.
type Handler struct{ svc DriverServicer }

// New returns a Handler wired to the given service.
func New(svc DriverServicer) *Handler { return &Handler{svc: svc} }

// Routes returns a chi.Router with all driver routes.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	r.Post("/register", h.Register)
	r.Post("/login", h.Login)
	r.Post("/verify-login", h.VerifyLogin)
	r.Post("/refresh", h.Refresh)

	r.Group(func(r chi.Router) {
		r.Use(jwt.RequireAuth)
		r.Use(jwt.RequireRole("driver"))
		r.Get("/nearby", h.GetNearby)
		r.Get("/{id}", h.GetByID)
		r.Patch("/{id}", h.UpdateProfile)
		r.Patch("/{id}/location", h.UpdateLocation)
		r.Patch("/{id}/status", h.UpdateStatus)
		r.Post("/trips/{tripId}/respond", h.RespondToOffer)
	})

	return r
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req model.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateName(req.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return
	}
	if !validation.ValidateEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if !validation.ValidatePhone(req.Phone) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid phone"})
		return
	}
	if !validation.ValidatePassword(req.Password) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 6 characters"})
		return
	}
	if strings.TrimSpace(req.LicensePlate) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "license_plate is required"})
		return
	}
	resp, err := h.svc.Register(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// Login validates credentials, sends OTP, and returns 202. No JWT yet.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if err := h.svc.Login(r.Context(), req); err != nil {
		if errors.Is(err, otp.ErrRateLimitExceeded) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "OTP sent to " + req.Email})
}

// VerifyLogin confirms the OTP and returns a JWT on success.
func (h *Handler) VerifyLogin(w http.ResponseWriter, r *http.Request) {
	var req model.VerifyLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if !sixDigits.MatchString(req.OTP) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "otp must be exactly 6 digits"})
		return
	}
	resp, err := h.svc.VerifyLogin(r.Context(), req)
	if err != nil {
		if errors.Is(err, otp.ErrMaxAttemptsExceeded) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req model.RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refresh_token is required"})
		return
	}
	resp, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := jwt.GetClaims(r.Context())
	if claims == nil || claims.UserID != id {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	var req model.UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Name != "" && !validation.ValidateName(req.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return
	}
	if req.Phone != "" && !validation.ValidatePhone(req.Phone) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid phone"})
		return
	}
	if req.VehicleType != "" && !validation.ValidateVehicleType(req.VehicleType) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vehicle_type must be go, x, or xl"})
		return
	}
	if req.LicensePlate != "" && strings.TrimSpace(req.LicensePlate) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid license_plate"})
		return
	}
	d, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "driver not found"})
		return
	}
	claims := jwt.GetClaims(r.Context())
	if claims == nil || claims.UserID != d.ID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) UpdateLocation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := jwt.GetClaims(r.Context())
	if claims == nil || claims.UserID != id {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	var loc model.LocationUpdate
	if err := json.NewDecoder(r.Body).Decode(&loc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateCoordinates(loc.Lat, loc.Lng) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid coordinates"})
		return
	}
	if err := h.svc.UpdateLocation(r.Context(), id, loc.Lat, loc.Lng); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "location_updated"})
}

func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := jwt.GetClaims(r.Context())
	if claims == nil || claims.UserID != id {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	var req model.StatusUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateDriverStatus(req.Status) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be available, busy, or offline"})
		return
	}
	d, err := h.svc.UpdateStatus(r.Context(), id, req.Status)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) GetNearby(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("lat")
	lngStr := r.URL.Query().Get("lng")
	if latStr == "" || lngStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "lat and lng are required"})
		return
	}
	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid lat"})
		return
	}
	lng, err := strconv.ParseFloat(lngStr, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid lng"})
		return
	}
	if !validation.ValidateCoordinates(lat, lng) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid coordinates"})
		return
	}
	radius := 5.0
	if v := r.URL.Query().Get("radius"); v != "" {
		radius, err = strconv.ParseFloat(v, 64)
		if err != nil || radius <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid radius"})
			return
		}
	}
	ids, err := h.svc.GetNearby(r.Context(), lat, lng, radius)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"drivers": ids})
}

func (h *Handler) RespondToOffer(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	tripID := chi.URLParam(r, "tripId")
	var req struct {
		Accept bool `json:"accept"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := h.svc.RespondToOffer(r.Context(), claims.UserID, tripID, req.Accept); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
