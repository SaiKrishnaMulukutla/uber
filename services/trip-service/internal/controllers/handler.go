package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"uber/shared/pkg/httputil"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/validation"
	"uber/trip-service/internal/model"
)

// TripServicer is the subset of service.TripService the handler needs.
type TripServicer interface {
	Request(ctx context.Context, riderID, riderEmail, riderPhone string, req model.TripRequest) (*model.Trip, error)
	GetByID(ctx context.Context, id string) (*model.Trip, error)
	AssignDriver(ctx context.Context, tripID, driverID string) (*model.Trip, error)
	Start(ctx context.Context, tripID, callerID, otp string) (*model.Trip, error)
	End(ctx context.Context, tripID, callerID string, distKm *float64) (*model.Trip, error)
	Cancel(ctx context.Context, tripID, callerID, reason string) (*model.Trip, error)
	ListByRider(ctx context.Context, riderID string, limit, offset int) (*model.HistoryResponse, error)
	ListByDriver(ctx context.Context, driverID string, limit, offset int) (*model.HistoryResponse, error)
	Estimate(ctx context.Context, pickupLat, pickupLng, dropLat, dropLng float64, vehicleType string) *model.EstimateResponse
	Rate(ctx context.Context, tripID, raterID, raterRole string, req model.RateRequest) (*model.Rating, error)
	PushLocation(ctx context.Context, tripID, driverID string, lat, lng float64) error
	GetSurge(ctx context.Context) float64
	SetSurge(ctx context.Context, multiplier float64) error
	GetRideOTP(ctx context.Context, tripID string) (string, error)
}

// LocationBroadcaster pushes driver coordinates to all WebSocket subscribers of a trip.
type LocationBroadcaster interface {
	BroadcastLocation(tripID string, lat, lng float64)
}

// Handler exposes trip HTTP endpoints.
type Handler struct {
	svc            TripServicer
	hub            LocationBroadcaster
	internalSecret string
}

// New returns a Handler wired to the given service and WebSocket hub.
func New(svc TripServicer, hub LocationBroadcaster, internalSecret string) *Handler {
	return &Handler{svc: svc, hub: hub, internalSecret: internalSecret}
}

// Routes returns a chi.Router with all trip routes.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	// Internal-only route: requires X-Internal-Secret header, not a user JWT.
	// Called by matching-service; must never be reachable from driver clients.
	r.Group(func(r chi.Router) {
		r.Use(h.requireInternalSecret)
		r.Patch("/{id}/assign", h.Assign)
	})

	r.Group(func(r chi.Router) {
		r.Use(jwt.RequireAuth)
		r.Use(jwt.RequireRole("admin"))
		r.Get("/surge", h.GetSurge)
		r.Patch("/surge", h.SetSurge)
	})

	r.Group(func(r chi.Router) {
		r.Use(jwt.RequireAuth)
		r.Use(jwt.RequireRole("rider", "driver"))

		r.Get("/history", h.History)
		r.Post("/estimate", h.Estimate)
		r.Post("/request", h.Request)
		r.Get("/{id}", h.GetByID)
		r.Patch("/{id}/cancel", h.Cancel)
		r.Patch("/{id}/start", h.Start)
		r.Patch("/{id}/end", h.End)
		r.Post("/{id}/rate", h.Rate)
		r.Post("/{id}/location", h.PushLocation)
	})

	return r
}

// requireInternalSecret is middleware that rejects requests without the correct
// X-Internal-Secret header. Fails closed: if the secret is not configured, all
// requests are rejected to prevent accidental open access in production.
func (h *Handler) requireInternalSecret(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.internalSecret == "" {
			httputil.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "internal secret not configured"})
			return
		}
		if r.Header.Get("X-Internal-Secret") != h.internalSecret {
			httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) Estimate(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "rider" {
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "only riders can request estimates"})
		return
	}
	var req model.EstimateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateCoordinates(req.PickupLat, req.PickupLng) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pickup coordinates"})
		return
	}
	if !validation.ValidateCoordinates(req.DropLat, req.DropLng) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid drop coordinates"})
		return
	}
	resp := h.svc.Estimate(r.Context(), req.PickupLat, req.PickupLng, req.DropLat, req.DropLng, req.VehicleType)
	httputil.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Rate(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	tripID := chi.URLParam(r, "id")
	var req model.RateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateRatingScore(req.Score) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "score must be between 1 and 5"})
		return
	}
	rating, err := h.svc.Rate(r.Context(), tripID, claims.UserID, claims.Role, req)
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, rating)
}

func (h *Handler) Request(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "rider" {
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "only riders can request trips"})
		return
	}
	var req model.TripRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateCoordinates(req.PickupLat, req.PickupLng) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pickup coordinates"})
		return
	}
	if !validation.ValidateCoordinates(req.DropLat, req.DropLng) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid drop coordinates"})
		return
	}
	trip, err := h.svc.Request(r.Context(), claims.UserID, claims.Email, claims.Phone, req)
	if err != nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, map[string]any{"trip_id": trip.ID, "status": trip.Status})
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.GetByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	claims := jwt.GetClaims(r.Context())
	if claims.UserID != t.RiderID && (t.DriverID == nil || claims.UserID != *t.DriverID) {
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "not your trip"})
		return
	}
	// Show the ride OTP only to the rider while the trip is awaiting start.
	if claims.UserID == t.RiderID && t.Status == model.StatusDriverAssigned {
		if otp, otpErr := h.svc.GetRideOTP(r.Context(), t.ID); otpErr == nil {
			t.RideOTP = &otp
		}
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

// Assign is an internal endpoint called by matching-service (not user clients).
// It is protected by X-Internal-Secret, not a user JWT.
func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	var req model.AssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.DriverID == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "driver_id is required"})
		return
	}
	t, err := h.svc.AssignDriver(r.Context(), chi.URLParam(r, "id"), req.DriverID)
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "driver" {
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "only drivers can start trips"})
		return
	}
	var req model.StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.OTP == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "otp is required"})
		return
	}
	t, err := h.svc.Start(r.Context(), chi.URLParam(r, "id"), claims.UserID, req.OTP)
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	var req model.CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if len(req.Reason) > 500 {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "reason must be 500 characters or fewer"})
		return
	}
	t, err := h.svc.Cancel(r.Context(), chi.URLParam(r, "id"), claims.UserID, req.Reason)
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) End(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "driver" {
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "only drivers can end trips"})
		return
	}
	var req model.EndRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	t, err := h.svc.End(r.Context(), chi.URLParam(r, "id"), claims.UserID, req.DistanceKm)
	if err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) PushLocation(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "driver" {
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "only drivers can push location"})
		return
	}
	tripID := chi.URLParam(r, "id")
	var req struct {
		Lat float64 `json:"lat"`
		Lng float64 `json:"lng"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateCoordinates(req.Lat, req.Lng) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid coordinates"})
		return
	}
	if err := h.svc.PushLocation(r.Context(), tripID, claims.UserID, req.Lat, req.Lng); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.hub.BroadcastLocation(tripID, req.Lat, req.Lng)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"message": "location updated"})
}

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	limit, offset := httputil.ParsePagination(r, 10)

	var resp *model.HistoryResponse
	var err error
	switch claims.Role {
	case "rider":
		resp, err = h.svc.ListByRider(r.Context(), claims.UserID, limit, offset)
	case "driver":
		resp, err = h.svc.ListByDriver(r.Context(), claims.UserID, limit, offset)
	default:
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "invalid role"})
		return
	}
	if err != nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}


func (h *Handler) GetSurge(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, map[string]float64{"multiplier": h.svc.GetSurge(r.Context())})
}

func (h *Handler) SetSurge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Multiplier float64 `json:"multiplier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	const (
		minSurge = 1.0
		maxSurge = 5.0
	)
	if req.Multiplier < minSurge || req.Multiplier > maxSurge {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "multiplier must be between 1.0 and 5.0"})
		return
	}
	if err := h.svc.SetSurge(r.Context(), req.Multiplier); err != nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]float64{"multiplier": req.Multiplier})
}

