package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"uber/trip-service/internal/model"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/validation"
)

// TripServicer is the subset of service.TripService the handler needs.
type TripServicer interface {
	Request(ctx context.Context, riderID, riderEmail string, req model.TripRequest) (*model.Trip, error)
	GetByID(ctx context.Context, id string) (*model.Trip, error)
	AssignDriver(ctx context.Context, tripID, driverID string) (*model.Trip, error)
	Start(ctx context.Context, tripID string) (*model.Trip, error)
	End(ctx context.Context, tripID string, distKm *float64) (*model.Trip, error)
	Cancel(ctx context.Context, tripID, reason string) (*model.Trip, error)
	ListByRider(ctx context.Context, riderID string, limit, offset int) (*model.HistoryResponse, error)
	ListByDriver(ctx context.Context, driverID string, limit, offset int) (*model.HistoryResponse, error)
	Estimate(ctx context.Context, pickupLat, pickupLng, dropLat, dropLng float64) *model.EstimateResponse
	Rate(ctx context.Context, tripID, raterID, raterRole string, req model.RateRequest) (*model.Rating, error)
	PushLocation(ctx context.Context, tripID, driverID string, lat, lng float64) error
}

// LocationBroadcaster pushes driver coordinates to all WebSocket subscribers of a trip.
type LocationBroadcaster interface {
	BroadcastLocation(tripID string, lat, lng float64)
}

// Handler exposes trip HTTP endpoints.
type Handler struct {
	svc TripServicer
	hub LocationBroadcaster
}

// New returns a Handler wired to the given service and WebSocket hub.
func New(svc TripServicer, hub LocationBroadcaster) *Handler { return &Handler{svc: svc, hub: hub} }

// Routes returns a chi.Router with all trip routes.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(jwt.RequireAuth)
	r.Use(jwt.RequireRole("rider", "driver"))

	r.Get("/history", h.History)
	r.Post("/estimate", h.Estimate)
	r.Post("/request", h.Request)
	r.Get("/{id}", h.GetByID)
	r.Patch("/{id}/assign", h.Assign)
	r.Patch("/{id}/cancel", h.Cancel)
	r.Patch("/{id}/start", h.Start)
	r.Patch("/{id}/end", h.End)
	r.Post("/{id}/rate", h.Rate)
	r.Post("/{id}/location", h.PushLocation)

	return r
}

func (h *Handler) Estimate(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "rider" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only riders can request estimates"})
		return
	}
	var req model.EstimateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateCoordinates(req.PickupLat, req.PickupLng) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pickup coordinates"})
		return
	}
	if !validation.ValidateCoordinates(req.DropLat, req.DropLng) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid drop coordinates"})
		return
	}
	resp := h.svc.Estimate(r.Context(), req.PickupLat, req.PickupLng, req.DropLat, req.DropLng)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Rate(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	tripID := chi.URLParam(r, "id")
	var req model.RateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateRatingScore(req.Score) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "score must be between 1 and 5"})
		return
	}
	rating, err := h.svc.Rate(r.Context(), tripID, claims.UserID, claims.Role, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, rating)
}

func (h *Handler) Request(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "rider" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only riders can request trips"})
		return
	}
	var req model.TripRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateCoordinates(req.PickupLat, req.PickupLng) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pickup coordinates"})
		return
	}
	if !validation.ValidateCoordinates(req.DropLat, req.DropLng) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid drop coordinates"})
		return
	}
	trip, err := h.svc.Request(r.Context(), claims.UserID, claims.Email, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"trip_id": trip.ID, "status": trip.Status})
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	t, err := h.svc.GetByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	claims := jwt.GetClaims(r.Context())
	if claims.UserID != t.RiderID && (t.DriverID == nil || claims.UserID != *t.DriverID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not your trip"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	var req model.AssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.DriverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "driverId is required"})
		return
	}
	t, err := h.svc.AssignDriver(r.Context(), chi.URLParam(r, "id"), req.DriverID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "driver" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only drivers can start trips"})
		return
	}
	t, err := h.svc.Start(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	var req model.CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if len(req.Reason) > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason must be 500 characters or fewer"})
		return
	}
	t, err := h.svc.Cancel(r.Context(), chi.URLParam(r, "id"), req.Reason)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) End(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "driver" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only drivers can end trips"})
		return
	}
	var req model.EndRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	t, err := h.svc.End(r.Context(), chi.URLParam(r, "id"), req.DistanceKm)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) PushLocation(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "driver" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only drivers can push location"})
		return
	}
	tripID := chi.URLParam(r, "id")
	var req struct {
		Lat float64 `json:"lat"`
		Lng float64 `json:"lng"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateCoordinates(req.Lat, req.Lng) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid coordinates"})
		return
	}
	if err := h.svc.PushLocation(r.Context(), tripID, claims.UserID, req.Lat, req.Lng); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.hub.BroadcastLocation(tripID, req.Lat, req.Lng)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	limit, offset := parsePagination(r)

	var resp *model.HistoryResponse
	var err error
	switch claims.Role {
	case "rider":
		resp, err = h.svc.ListByRider(r.Context(), claims.UserID, limit, offset)
	case "driver":
		resp, err = h.svc.ListByDriver(r.Context(), claims.UserID, limit, offset)
	default:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid role"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func parsePagination(r *http.Request) (limit, offset int) {
	limit = 10
	offset = 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 {
			limit = l
		}
	}
	if limit > 50 {
		limit = 50
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if o, err := strconv.Atoi(v); err == nil && o >= 0 {
			offset = o
		}
	}
	return
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
