package trips

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"uber/shared/pkg/jwt"
	"uber/shared/pkg/validation"
)

// TripService defines the business operations the handler depends on.
type TripService interface {
	Request(ctx context.Context, riderID string, req TripRequest) (*Trip, error)
	GetByID(ctx context.Context, id string) (*Trip, error)
	AssignDriver(ctx context.Context, tripID, driverID string) (*Trip, error)
	Start(ctx context.Context, tripID string) (*Trip, error)
	End(ctx context.Context, tripID string, distKm *float64) (*Trip, error)
	Cancel(ctx context.Context, tripID, reason string) (*Trip, error)
	ListByRider(ctx context.Context, riderID string, limit, offset int) (*HistoryResponse, error)
	ListByDriver(ctx context.Context, driverID string, limit, offset int) (*HistoryResponse, error)
	Estimate(pickupLat, pickupLng, dropLat, dropLng float64) *EstimateResponse
	Rate(ctx context.Context, tripID, raterID, raterRole string, req RateRequest) (*Rating, error)
}

// Handler exposes trip HTTP endpoints.
type Handler struct{ svc TripService }

// NewHandler wires a handler to the trip service.
func NewHandler(svc TripService) *Handler { return &Handler{svc: svc} }

// Routes returns a chi.Router with all trip routes.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(jwt.RequireAuth)
	r.Use(jwt.RequireRole("rider", "driver"))

	r.Get("/history", h.History) // before /{id}
	r.Post("/estimate", h.Estimate)
	r.Post("/request", h.Request)
	r.Get("/{id}", h.GetByID)
	r.Patch("/{id}/assign", h.Assign)
	r.Patch("/{id}/cancel", h.Cancel)
	r.Patch("/{id}/start", h.Start)
	r.Patch("/{id}/end", h.End)
	r.Post("/{id}/rate", h.Rate)

	return r
}

func (h *Handler) Estimate(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "rider" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only riders can request estimates"})
		return
	}
	var req EstimateRequest
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
	resp := h.svc.Estimate(req.PickupLat, req.PickupLng, req.DropLat, req.DropLng)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) Rate(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	tripID := chi.URLParam(r, "id")
	var req RateRequest
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

	var req TripRequest
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

	trip, err := h.svc.Request(r.Context(), claims.UserID, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"trip_id": trip.ID,
		"status":  trip.Status,
	})
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
	var req AssignRequest
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
	var req CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
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
	var req EndRequest
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

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	limit, offset := parsePagination(r)

	var resp *HistoryResponse
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
