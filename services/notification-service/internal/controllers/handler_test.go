package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"uber/notification-service/internal/model"
	"uber/shared/pkg/jwt"
)

// ---------- mock ----------

type mockNotifRepo struct {
	ListByUserFn func(ctx context.Context, userID string, limit, offset int) ([]*model.Notification, int, error)
	MarkReadFn   func(ctx context.Context, id, userID string) error
}

func (m *mockNotifRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*model.Notification, int, error) {
	return m.ListByUserFn(ctx, userID, limit, offset)
}
func (m *mockNotifRepo) MarkRead(ctx context.Context, id, userID string) error {
	return m.MarkReadFn(ctx, id, userID)
}

// ---------- helpers ----------

func TestMain(m *testing.M) {
	jwt.Init("test-secret")
	os.Exit(m.Run())
}

func notifRouter(mock *mockNotifRepo) http.Handler {
	h := NewHandler(mock)
	r := chi.NewRouter()
	r.Use(jwt.OptionalAuth)
	r.Mount("/notifications", h.Routes())
	return r
}

func doRequest(router http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// ---------- tests ----------

func TestList_Success(t *testing.T) {
	now := time.Now()
	mock := &mockNotifRepo{
		ListByUserFn: func(_ context.Context, userID string, limit, offset int) ([]*model.Notification, int, error) {
			return []*model.Notification{
				{ID: "n1", UserID: userID, Type: "trip_completed", Title: "Trip Done", Body: "Fare: 100", CreatedAt: now},
			}, 1, nil
		},
	}
	router := notifRouter(mock)

	token, _ := jwt.Generate("u1", "rider@example.com", "rider")
	rec := doRequest(router, http.MethodGet, "/notifications/", nil, token)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp model.NotificationListResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Total != 1 {
		t.Fatalf("expected total=1, got %d", resp.Total)
	}
}

func TestList_RequiresAuth(t *testing.T) {
	mock := &mockNotifRepo{}
	router := notifRouter(mock)

	rec := doRequest(router, http.MethodGet, "/notifications/", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMarkRead_Success(t *testing.T) {
	mock := &mockNotifRepo{
		MarkReadFn: func(_ context.Context, id, userID string) error {
			return nil
		},
	}
	router := notifRouter(mock)

	token, _ := jwt.Generate("u1", "rider@example.com", "rider")
	rec := doRequest(router, http.MethodPatch, "/notifications/n1/read", nil, token)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
