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
	RegisterFn       func(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error)
	LoginFn          func(ctx context.Context, req model.LoginRequest) error
	VerifyLoginFn    func(ctx context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error)
	RefreshFn        func(ctx context.Context, refreshToken string) (*model.RefreshResponse, error)
	GetByIDFn        func(ctx context.Context, id string) (*model.Driver, error)
	UpdateLocationFn func(ctx context.Context, driverID string, lat, lng float64) error
	UpdateStatusFn   func(ctx context.Context, driverID, status string) (*model.Driver, error)
	GetNearbyFn      func(ctx context.Context, lat, lng, radiusKm float64) ([]string, error)
}

func (m *mockDriverService) Register(ctx context.Context, req model.RegisterRequest) (*model.AuthResponse, error) {
	return m.RegisterFn(ctx, req)
}
func (m *mockDriverService) Login(ctx context.Context, req model.LoginRequest) error {
	return m.LoginFn(ctx, req)
}
func (m *mockDriverService) VerifyLogin(ctx context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error) {
	return m.VerifyLoginFn(ctx, req)
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
		RegisterFn: func(_ context.Context, req model.RegisterRequest) (*model.AuthResponse, error) {
			return &model.AuthResponse{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				Driver:       &model.Driver{ID: "d1", Name: req.Name, Email: req.Email, Phone: req.Phone, Status: "available", Rating: 5.0},
			}, nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/register", model.RegisterRequest{
		Name: "Bob", Email: "bob@example.com", Phone: "+1234567890", Password: "secret123",
		VehicleType: "sedan", LicensePlate: "ABC-123",
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

func TestLogin_Success(t *testing.T) {
	mock := &mockDriverService{
		LoginFn: func(_ context.Context, req model.LoginRequest) error {
			return nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/login", model.LoginRequest{
		Email: "bob@example.com", Password: "secret123",
	}, "")

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLogin_InvalidCredentials(t *testing.T) {
	mock := &mockDriverService{
		LoginFn: func(_ context.Context, req model.LoginRequest) error {
			return errors.New("invalid credentials")
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/login", model.LoginRequest{
		Email: "bob@example.com", Password: "wrong",
	}, "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVerifyLogin_Success(t *testing.T) {
	mock := &mockDriverService{
		VerifyLoginFn: func(_ context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error) {
			return &model.AuthResponse{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				Driver:       &model.Driver{ID: "d1", Email: req.Email},
			}, nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/verify-login", model.VerifyLoginRequest{
		Email: "bob@example.com", OTP: "123456",
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

func TestVerifyLogin_InvalidOTP(t *testing.T) {
	mock := &mockDriverService{
		VerifyLoginFn: func(_ context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error) {
			return nil, otp.ErrInvalidOTP
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/verify-login", model.VerifyLoginRequest{
		Email: "bob@example.com", OTP: "000000",
	}, "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVerifyLogin_MaxAttempts(t *testing.T) {
	mock := &mockDriverService{
		VerifyLoginFn: func(_ context.Context, req model.VerifyLoginRequest) (*model.AuthResponse, error) {
			return nil, otp.ErrMaxAttemptsExceeded
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/verify-login", model.VerifyLoginRequest{
		Email: "bob@example.com", OTP: "000000",
	}, "")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
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
