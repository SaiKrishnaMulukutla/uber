package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"uber/shared/pkg/jwt"
	"uber/payment-service/internal/model"
)

type PaymentServicer interface {
	GetByTripID(ctx context.Context, tripID string) (*model.Payment, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) (*model.PaymentHistoryResponse, error)
}

type Handler struct{ svc PaymentServicer }

func NewHandler(svc PaymentServicer) *Handler { return &Handler{svc: svc} }

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(jwt.RequireAuth)
	r.Use(jwt.RequireRole("rider", "driver"))
	r.Get("/history", h.History)
	r.Get("/{tripId}", h.GetByTripID)
	return r
}

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	limit, offset := parsePagination(r)
	resp, err := h.svc.ListByUser(r.Context(), claims.UserID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetByTripID(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	tripID := chi.URLParam(r, "tripId")
	p, err := h.svc.GetByTripID(r.Context(), tripID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if claims.UserID != p.RiderID && claims.UserID != p.DriverID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not your payment"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func parsePagination(r *http.Request) (limit, offset int) {
	limit = 20
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
