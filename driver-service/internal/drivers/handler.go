package drivers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"uber/shared/pkg/jwt"
	"uber/shared/pkg/validation"
)

// DriverService defines the business operations the handler depends on.
type DriverService interface {
	Register(ctx context.Context, req RegisterRequest) (*AuthResponse, error)
	Login(ctx context.Context, req LoginRequest) (*AuthResponse, error)
	Refresh(ctx context.Context, refreshToken string) (*RefreshResponse, error)
	GetByID(ctx context.Context, id string) (*Driver, error)
	UpdateLocation(ctx context.Context, driverID string, lat, lng float64) error
	UpdateStatus(ctx context.Context, driverID, status string) (*Driver, error)
	GetNearby(ctx context.Context, lat, lng, radiusKm float64) ([]string, error)
}

// Handler exposes driver HTTP endpoints.
type Handler struct{ svc DriverService }

// NewHandler wires a handler to the driver service.
func NewHandler(svc DriverService) *Handler { return &Handler{svc: svc} }

// Routes returns a chi.Router with all driver routes.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	// Public
	r.Post("/register", h.Register)
	r.Post("/login", h.Login)
	r.Post("/refresh", h.Refresh)

	// Protected — drivers only
	r.Group(func(r chi.Router) {
		r.Use(jwt.RequireAuth)
		r.Use(jwt.RequireRole("driver"))
		r.Get("/nearby", h.GetNearby) // must come before /{id}
		r.Get("/{id}", h.GetByID)
		r.Patch("/{id}/location", h.UpdateLocation)
		r.Patch("/{id}/status", h.UpdateStatus)
	})

	return r
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
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
	resp, err := h.svc.Register(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	resp, err := h.svc.Login(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
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

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	d, err := h.svc.GetByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *Handler) UpdateLocation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var loc LocationUpdate
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

func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req StatusUpdate
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
