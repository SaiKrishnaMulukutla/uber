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

	"uber/trip-service/internal/model"
	"uber/shared/pkg/jwt"
)

// ---------- mock ----------

type mockTripService struct {
	RequestFn      func(ctx context.Context, riderID, riderEmail string, req model.TripRequest) (*model.Trip, error)
	GetByIDFn      func(ctx context.Context, id string) (*model.Trip, error)
	AssignDriverFn func(ctx context.Context, tripID, driverID string) (*model.Trip, error)
	StartFn        func(ctx context.Context, tripID, callerID string) (*model.Trip, error)
	EndFn          func(ctx context.Context, tripID, callerID string, distKm *float64) (*model.Trip, error)
	CancelFn       func(ctx context.Context, tripID, callerID, reason string) (*model.Trip, error)
	ListByRiderFn  func(ctx context.Context, riderID string, limit, offset int) (*model.HistoryResponse, error)
	ListByDriverFn func(ctx context.Context, driverID string, limit, offset int) (*model.HistoryResponse, error)
	EstimateFn      func(ctx context.Context, pickupLat, pickupLng, dropLat, dropLng float64) *model.EstimateResponse
	RateFn          func(ctx context.Context, tripID, raterID, raterRole string, req model.RateRequest) (*model.Rating, error)
	PushLocationFn  func(ctx context.Context, tripID, driverID string, lat, lng float64) error
}

func (m *mockTripService) Request(ctx context.Context, riderID, riderEmail string, req model.TripRequest) (*model.Trip, error) {
	return m.RequestFn(ctx, riderID, riderEmail, req)
}
func (m *mockTripService) GetByID(ctx context.Context, id string) (*model.Trip, error) {
	return m.GetByIDFn(ctx, id)
}
func (m *mockTripService) AssignDriver(ctx context.Context, tripID, driverID string) (*model.Trip, error) {
	return m.AssignDriverFn(ctx, tripID, driverID)
}
func (m *mockTripService) Start(ctx context.Context, tripID, callerID string) (*model.Trip, error) {
	return m.StartFn(ctx, tripID, callerID)
}
func (m *mockTripService) End(ctx context.Context, tripID, callerID string, distKm *float64) (*model.Trip, error) {
	return m.EndFn(ctx, tripID, callerID, distKm)
}
func (m *mockTripService) Cancel(ctx context.Context, tripID, callerID, reason string) (*model.Trip, error) {
	return m.CancelFn(ctx, tripID, callerID, reason)
}
func (m *mockTripService) ListByRider(ctx context.Context, riderID string, limit, offset int) (*model.HistoryResponse, error) {
	return m.ListByRiderFn(ctx, riderID, limit, offset)
}
func (m *mockTripService) ListByDriver(ctx context.Context, driverID string, limit, offset int) (*model.HistoryResponse, error) {
	return m.ListByDriverFn(ctx, driverID, limit, offset)
}
func (m *mockTripService) Estimate(ctx context.Context, pickupLat, pickupLng, dropLat, dropLng float64) *model.EstimateResponse {
	return m.EstimateFn(ctx, pickupLat, pickupLng, dropLat, dropLng)
}
func (m *mockTripService) Rate(ctx context.Context, tripID, raterID, raterRole string, req model.RateRequest) (*model.Rating, error) {
	return m.RateFn(ctx, tripID, raterID, raterRole, req)
}
func (m *mockTripService) PushLocation(ctx context.Context, tripID, driverID string, lat, lng float64) error {
	if m.PushLocationFn != nil {
		return m.PushLocationFn(ctx, tripID, driverID, lat, lng)
	}
	return nil
}

type noopHub struct{}

func (noopHub) BroadcastLocation(_ string, _, _ float64) {}

// ---------- helpers ----------

func TestMain(m *testing.M) {
	jwt.Init("test-secret")
	os.Exit(m.Run())
}

func tripRouter(mock *mockTripService) http.Handler {
	h := New(mock, noopHub{}, "")
	r := chi.NewRouter()
	r.Use(jwt.OptionalAuth)
	r.Mount("/trips", h.Routes())
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

func TestRequest_RequiresRiderRole(t *testing.T) {
	mock := &mockTripService{
		RequestFn: func(_ context.Context, riderID, riderEmail string, req model.TripRequest) (*model.Trip, error) {
			return &model.Trip{ID: "t1", RiderID: riderID, Status: "REQUESTED"}, nil
		},
	}
	router := tripRouter(mock)

	driverToken, err := jwt.Generate("d1", "driver@example.com", "driver")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodPost, "/trips/request", model.TripRequest{
		PickupLat: 12.97, PickupLng: 77.59, DropLat: 12.93, DropLng: 77.63,
	}, driverToken)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStart_RequiresDriverRole(t *testing.T) {
	mock := &mockTripService{
		StartFn: func(_ context.Context, tripID, callerID string) (*model.Trip, error) {
			return &model.Trip{ID: tripID, Status: "STARTED"}, nil
		},
	}
	router := tripRouter(mock)

	riderToken, err := jwt.Generate("u1", "rider@example.com", "rider")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodPatch, "/trips/t1/start", nil, riderToken)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHistory_RiderSeeOwnTrips(t *testing.T) {
	now := time.Now()
	mock := &mockTripService{
		ListByRiderFn: func(_ context.Context, riderID string, limit, offset int) (*model.HistoryResponse, error) {
			return &model.HistoryResponse{
				Trips: []*model.Trip{
					{ID: "t1", RiderID: riderID, Status: "COMPLETED", CreatedAt: now},
					{ID: "t2", RiderID: riderID, Status: "REQUESTED", CreatedAt: now},
				},
				Total:  2,
				Limit:  limit,
				Offset: offset,
			}, nil
		},
	}
	router := tripRouter(mock)

	riderToken, err := jwt.Generate("u1", "rider@example.com", "rider")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodGet, "/trips/history", nil, riderToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp model.HistoryResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Total != 2 {
		t.Fatalf("expected total=2, got %d", resp.Total)
	}
	if len(resp.Trips) != 2 {
		t.Fatalf("expected 2 trips, got %d", len(resp.Trips))
	}
}

func TestHistory_PaginationDefaults(t *testing.T) {
	var capturedLimit, capturedOffset int
	mock := &mockTripService{
		ListByRiderFn: func(_ context.Context, riderID string, limit, offset int) (*model.HistoryResponse, error) {
			capturedLimit = limit
			capturedOffset = offset
			return &model.HistoryResponse{Trips: []*model.Trip{}, Total: 0, Limit: limit, Offset: offset}, nil
		},
	}
	router := tripRouter(mock)

	riderToken, err := jwt.Generate("u1", "rider@example.com", "rider")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodGet, "/trips/history", nil, riderToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedLimit != 10 {
		t.Fatalf("expected default limit=10, got %d", capturedLimit)
	}
	if capturedOffset != 0 {
		t.Fatalf("expected default offset=0, got %d", capturedOffset)
	}
}

func TestEstimate_Success(t *testing.T) {
	mock := &mockTripService{
		EstimateFn: func(_ context.Context, pickupLat, pickupLng, dropLat, dropLng float64) *model.EstimateResponse {
			return &model.EstimateResponse{
				EstimatedFare: 110.0, EstimatedDistance: 5.0,
				EstimatedDuration: 12.0, SurgeMultiplier: 1.0, Currency: "INR",
			}
		},
	}
	router := tripRouter(mock)

	riderToken, err := jwt.Generate("u1", "rider@example.com", "rider")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodPost, "/trips/estimate", model.EstimateRequest{
		PickupLat: 12.97, PickupLng: 77.59, DropLat: 12.93, DropLng: 77.63,
	}, riderToken)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp model.EstimateResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Currency != "INR" {
		t.Fatalf("expected currency=INR, got %s", resp.Currency)
	}
}

func TestEstimate_RequiresRiderRole(t *testing.T) {
	mock := &mockTripService{
		EstimateFn: func(_ context.Context, pickupLat, pickupLng, dropLat, dropLng float64) *model.EstimateResponse {
			return &model.EstimateResponse{}
		},
	}
	router := tripRouter(mock)

	driverToken, err := jwt.Generate("d1", "driver@example.com", "driver")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodPost, "/trips/estimate", model.EstimateRequest{
		PickupLat: 12.97, PickupLng: 77.59, DropLat: 12.93, DropLng: 77.63,
	}, driverToken)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRate_Success(t *testing.T) {
	mock := &mockTripService{
		RateFn: func(_ context.Context, tripID, raterID, raterRole string, req model.RateRequest) (*model.Rating, error) {
			return &model.Rating{
				ID: "r1", TripID: tripID, RaterID: raterID, RaterRole: raterRole,
				RateeID: "d1", RateeRole: "driver", Score: req.Score, CreatedAt: time.Now(),
			}, nil
		},
	}
	router := tripRouter(mock)

	riderToken, err := jwt.Generate("u1", "rider@example.com", "rider")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodPost, "/trips/t1/rate", model.RateRequest{Score: 5, Comment: "Great!"}, riderToken)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRate_InvalidScore(t *testing.T) {
	mock := &mockTripService{}
	router := tripRouter(mock)

	riderToken, err := jwt.Generate("u1", "rider@example.com", "rider")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodPost, "/trips/t1/rate", model.RateRequest{Score: 6}, riderToken)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
