package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"uber/notification-service/internal/model"
	"uber/shared/pkg/httputil"
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
	limit, offset := httputil.ParsePagination(r, 20)
	notifs, total, err := h.repo.ListByUser(r.Context(), claims.UserID, limit, offset)
	if err != nil {
		httputil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, &model.NotificationListResponse{
		Notifications: notifs, Total: total, Limit: limit, Offset: offset,
	})
}

func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	id := chi.URLParam(r, "id")
	if err := h.repo.MarkRead(r.Context(), id, claims.UserID); err != nil {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "read"})
}
