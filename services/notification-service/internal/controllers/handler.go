package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"uber/notification-service/internal/model"
	"uber/shared/pkg/jwt"
)

type notifRepo interface {
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]*model.Notification, int, error)
	MarkRead(ctx context.Context, id, userID string) error
}

type Handler struct{ repo notifRepo }

func NewHandler(repo notifRepo) *Handler { return &Handler{repo: repo} }

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(jwt.RequireAuth)
	r.Use(jwt.RequireRole("rider", "driver"))
	r.Get("/", h.List)
	r.Patch("/{id}/read", h.MarkRead)
	return r
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	limit, offset := parsePagination(r)
	notifs, total, err := h.repo.ListByUser(r.Context(), claims.UserID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, &model.NotificationListResponse{
		Notifications: notifs, Total: total, Limit: limit, Offset: offset,
	})
}

func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	id := chi.URLParam(r, "id")
	if err := h.repo.MarkRead(r.Context(), id, claims.UserID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "read"})
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
