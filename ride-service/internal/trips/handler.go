package trips

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"ride-service/pkg/jwt"
)

// Handler exposes trip HTTP endpoints.
type Handler struct{ svc *Service }

// NewHandler wires a handler to the trip service.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes returns a chi.Router with all trip routes.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(jwt.RequireAuth) // all trip endpoints need auth

	r.Post("/request", h.Request)
	r.Get("/{id}", h.GetByID)
	r.Patch("/{id}/assign", h.Assign)
	r.Patch("/{id}/start", h.Start)
	r.Patch("/{id}/end", h.End)

	return r
}

func (h *Handler) Request(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())

	var req TripRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
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
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	var req AssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
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
	t, err := h.svc.Start(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) End(w http.ResponseWriter, r *http.Request) {
	var req EndRequest
	// body is optional
	json.NewDecoder(r.Body).Decode(&req)

	t, err := h.svc.End(r.Context(), chi.URLParam(r, "id"), req.DistanceKm)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
