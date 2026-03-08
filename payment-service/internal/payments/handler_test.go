package payments

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

	"uber/shared/pkg/jwt"
)

// ---------- mock ----------

type mockPaymentService struct {
	GetByTripIDFn func(ctx context.Context, tripID string) (*Payment, error)
	ListByUserFn  func(ctx context.Context, userID string, limit, offset int) (*PaymentHistoryResponse, error)
}

func (m *mockPaymentService) GetByTripID(ctx context.Context, tripID string) (*Payment, error) {
	return m.GetByTripIDFn(ctx, tripID)
}
func (m *mockPaymentService) ListByUser(ctx context.Context, userID string, limit, offset int) (*PaymentHistoryResponse, error) {
	return m.ListByUserFn(ctx, userID, limit, offset)
}

// ---------- helpers ----------

func TestMain(m *testing.M) {
	jwt.Init("test-secret")
	os.Exit(m.Run())
}

func paymentRouter(mock *mockPaymentService) http.Handler {
	h := NewHandler(mock)
	r := chi.NewRouter()
	r.Use(jwt.OptionalAuth)
	r.Mount("/payments", h.Routes())
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

func TestHistory_Success(t *testing.T) {
	now := time.Now()
	mock := &mockPaymentService{
		ListByUserFn: func(_ context.Context, userID string, limit, offset int) (*PaymentHistoryResponse, error) {
			return &PaymentHistoryResponse{
				Payments: []*Payment{
					{ID: "p1", TripID: "t1", RiderID: userID, DriverID: "d1", Amount: 110, Status: StatusCompleted, PaymentMethod: "cash", CreatedAt: now},
				},
				Total: 1, Limit: limit, Offset: offset,
			}, nil
		},
	}
	router := paymentRouter(mock)

	token, _ := jwt.Generate("u1", "rider@example.com", "rider")
	rec := doRequest(router, http.MethodGet, "/payments/history", nil, token)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp PaymentHistoryResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Total != 1 {
		t.Fatalf("expected total=1, got %d", resp.Total)
	}
}

func TestGetByTripID_OwnershipCheck(t *testing.T) {
	mock := &mockPaymentService{
		GetByTripIDFn: func(_ context.Context, tripID string) (*Payment, error) {
			return &Payment{
				ID: "p1", TripID: tripID, RiderID: "other-user", DriverID: "d1",
				Amount: 100, Status: StatusCompleted, PaymentMethod: "cash", CreatedAt: time.Now(),
			}, nil
		},
	}
	router := paymentRouter(mock)

	token, _ := jwt.Generate("u1", "rider@example.com", "rider")
	rec := doRequest(router, http.MethodGet, "/payments/t1", nil, token)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHistory_RequiresAuth(t *testing.T) {
	mock := &mockPaymentService{}
	router := paymentRouter(mock)

	rec := doRequest(router, http.MethodGet, "/payments/history", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
