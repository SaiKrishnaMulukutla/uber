package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"

	"uber/driver-service/internal/model"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/otp"
)

// ---------- mock ----------

type mockDriverService struct {
	RegisterFn         func(ctx context.Context, req model.RegisterRequest) error
	VerifyRegisterFn   func(ctx context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error)
	LoginFn            func(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error)
	RefreshFn          func(ctx context.Context, refreshToken string) (*model.RefreshResponse, error)
	GetByIDFn          func(ctx context.Context, id string) (*model.Driver, error)
	UpdateLocationFn   func(ctx context.Context, driverID string, lat, lng float64) error
	UpdateStatusFn     func(ctx context.Context, driverID, status string) (*model.Driver, error)
	GetNearbyFn        func(ctx context.Context, lat, lng, radiusKm float64) ([]string, error)
	RespondToOfferFn   func(ctx context.Context, driverID, tripID string, accept bool) error
	UpdateFn           func(ctx context.Context, id string, req model.UpdateRequest) (*model.Driver, error)
}

func (m *mockDriverService) Register(ctx context.Context, req model.RegisterRequest) error {
	return m.RegisterFn(ctx, req)
}
func (m *mockDriverService) VerifyRegister(ctx context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
	return m.VerifyRegisterFn(ctx, req)
}
func (m *mockDriverService) Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
	return m.LoginFn(ctx, req)
}
func (m *mockDriverService) Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error) {
	return m.RefreshFn(ctx, refreshToken)
}
func (m *mockDriverService) GetByID(ctx context.Context, id string) (*model.Driver, error) {
	return m.GetByIDFn(ctx, id)
}
func (m *mockDriverService) UpdateLocation(ctx context.Context, driverID string, lat, lng float64) error {
	return m.UpdateLocationFn(ctx, driverID, lat, lng)
}
func (m *mockDriverService) UpdateStatus(ctx context.Context, driverID, status string) (*model.Driver, error) {
	return m.UpdateStatusFn(ctx, driverID, status)
}
func (m *mockDriverService) GetNearby(ctx context.Context, lat, lng, radiusKm float64) ([]string, error) {
	return m.GetNearbyFn(ctx, lat, lng, radiusKm)
}
func (m *mockDriverService) RespondToOffer(ctx context.Context, driverID, tripID string, accept bool) error {
	if m.RespondToOfferFn != nil {
		return m.RespondToOfferFn(ctx, driverID, tripID, accept)
	}
	return nil
}
func (m *mockDriverService) Update(ctx context.Context, id string, req model.UpdateRequest) (*model.Driver, error) {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, id, req)
	}
	return nil, nil
}

// ---------- helpers ----------

func TestMain(m *testing.M) {
	jwt.Init("test-secret")
	os.Exit(m.Run())
}

func driverRouter(mock *mockDriverService) http.Handler {
	h := New(mock)
	r := chi.NewRouter()
	r.Use(jwt.OptionalAuth)
	r.Mount("/drivers", h.Routes())
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

func TestRegister_Success(t *testing.T) {
	mock := &mockDriverService{
		RegisterFn: func(_ context.Context, req model.RegisterRequest) error {
			return nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/register", model.RegisterRequest{
		Name: "Bob", Email: "bob@example.com", Phone: "+1234567890", Password: "secret123",
		VehicleType: "x", LicensePlate: "ABC-123",
	}, "")

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVerifyRegister_Success(t *testing.T) {
	mock := &mockDriverService{
		VerifyRegisterFn: func(_ context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
			return &model.AuthResponse{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				Driver:       &model.Driver{ID: "d1", Email: req.Email},
			}, nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/verify-register", model.VerifyRegisterRequest{
		Email: "bob@example.com", OTP: "123456",
	}, "")

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp model.AuthResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.AccessToken != "access-tok" {
		t.Fatalf("unexpected access token: %s", resp.AccessToken)
	}
}

func TestVerifyRegister_InvalidOTP(t *testing.T) {
	mock := &mockDriverService{
		VerifyRegisterFn: func(_ context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
			return nil, otp.ErrInvalidOTP
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/verify-register", model.VerifyRegisterRequest{
		Email: "bob@example.com", OTP: "000000",
	}, "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVerifyRegister_MaxAttempts(t *testing.T) {
	mock := &mockDriverService{
		VerifyRegisterFn: func(_ context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
			return nil, otp.ErrMaxAttemptsExceeded
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/verify-register", model.VerifyRegisterRequest{
		Email: "bob@example.com", OTP: "000000",
	}, "")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLogin_Success(t *testing.T) {
	mock := &mockDriverService{
		LoginFn: func(_ context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
			return &model.AuthResponse{AccessToken: "access-tok", RefreshToken: "refresh-tok"}, nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/login", model.LoginRequest{
		Email: "bob@example.com", Password: "secret123",
	}, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp model.AuthResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.AccessToken != "access-tok" {
		t.Fatalf("unexpected access token: %s", resp.AccessToken)
	}
}

func TestLogin_InvalidCredentials(t *testing.T) {
	mock := &mockDriverService{
		LoginFn: func(_ context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
			return nil, errors.New("invalid credentials")
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/login", model.LoginRequest{
		Email: "bob@example.com", Password: "wrongpass",
	}, "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRefresh_Success(t *testing.T) {
	mock := &mockDriverService{
		RefreshFn: func(_ context.Context, token string) (*model.RefreshResponse, error) {
			return &model.RefreshResponse{AccessToken: "new-access", RefreshToken: "new-refresh"}, nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/refresh", model.RefreshRequest{
		RefreshToken: "some-token",
	}, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp model.RefreshResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.AccessToken != "new-access" {
		t.Fatalf("unexpected access token: %s", resp.AccessToken)
	}
}

func TestRespondToOffer_Accept(t *testing.T) {
	var capturedDriverID, capturedTripID string
	var capturedAccept bool

	mock := &mockDriverService{
		RespondToOfferFn: func(_ context.Context, driverID, tripID string, accept bool) error {
			capturedDriverID = driverID
			capturedTripID = tripID
			capturedAccept = accept
			return nil
		},
	}
	router := driverRouter(mock)

	tok, _ := jwt.Generate("driver-1", "d@example.com", "driver")
	rec := doRequest(router, http.MethodPost, "/drivers/trips/trip-99/respond", map[string]bool{"accept": true}, tok)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedDriverID != "driver-1" {
		t.Errorf("expected driverID driver-1, got %s", capturedDriverID)
	}
	if capturedTripID != "trip-99" {
		t.Errorf("expected tripID trip-99, got %s", capturedTripID)
	}
	if !capturedAccept {
		t.Error("expected accept=true")
	}
}

func TestRespondToOffer_Reject(t *testing.T) {
	var capturedAccept bool

	mock := &mockDriverService{
		RespondToOfferFn: func(_ context.Context, _, _ string, accept bool) error {
			capturedAccept = accept
			return nil
		},
	}
	router := driverRouter(mock)

	tok, _ := jwt.Generate("driver-1", "d@example.com", "driver")
	rec := doRequest(router, http.MethodPost, "/drivers/trips/trip-99/respond", map[string]bool{"accept": false}, tok)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedAccept {
		t.Error("expected accept=false")
	}
}

func TestRespondToOffer_NoOffer(t *testing.T) {
	mock := &mockDriverService{
		RespondToOfferFn: func(_ context.Context, _, _ string, _ bool) error {
			return errors.New("no pending offer for this trip")
		},
	}
	router := driverRouter(mock)

	tok, _ := jwt.Generate("driver-1", "d@example.com", "driver")
	rec := doRequest(router, http.MethodPost, "/drivers/trips/trip-99/respond", map[string]bool{"accept": true}, tok)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRespondToOffer_RequiresAuth(t *testing.T) {
	mock := &mockDriverService{}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/trips/trip-99/respond", map[string]bool{"accept": true}, "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRespondToOffer_RequiresDriverRole(t *testing.T) {
	mock := &mockDriverService{}
	router := driverRouter(mock)

	riderToken, _ := jwt.Generate("u1", "rider@example.com", "rider")
	rec := doRequest(router, http.MethodPost, "/drivers/trips/trip-99/respond", map[string]bool{"accept": true}, riderToken)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateStatus_RequiresDriverRole(t *testing.T) {
	mock := &mockDriverService{
		UpdateStatusFn: func(_ context.Context, id, status string) (*model.Driver, error) {
			return &model.Driver{ID: id, Status: status}, nil
		},
	}
	router := driverRouter(mock)

	riderToken, err := jwt.Generate("u1", "rider@example.com", "rider")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodPatch, "/drivers/d1/status", model.StatusUpdate{
		Status: "available",
	}, riderToken)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
