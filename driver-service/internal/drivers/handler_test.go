package drivers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"

	"uber/shared/pkg/jwt"
)

// ---------- mock ----------

type mockDriverService struct {
	RegisterFn       func(ctx context.Context, req RegisterRequest) (*AuthResponse, error)
	LoginFn          func(ctx context.Context, req LoginRequest) (*AuthResponse, error)
	RefreshFn        func(ctx context.Context, refreshToken string) (*RefreshResponse, error)
	GetByIDFn        func(ctx context.Context, id string) (*Driver, error)
	UpdateLocationFn func(ctx context.Context, driverID string, lat, lng float64) error
	UpdateStatusFn   func(ctx context.Context, driverID, status string) (*Driver, error)
	GetNearbyFn      func(ctx context.Context, lat, lng, radiusKm float64) ([]string, error)
}

func (m *mockDriverService) Register(ctx context.Context, req RegisterRequest) (*AuthResponse, error) {
	return m.RegisterFn(ctx, req)
}
func (m *mockDriverService) Login(ctx context.Context, req LoginRequest) (*AuthResponse, error) {
	return m.LoginFn(ctx, req)
}
func (m *mockDriverService) Refresh(ctx context.Context, refreshToken string) (*RefreshResponse, error) {
	return m.RefreshFn(ctx, refreshToken)
}
func (m *mockDriverService) GetByID(ctx context.Context, id string) (*Driver, error) {
	return m.GetByIDFn(ctx, id)
}
func (m *mockDriverService) UpdateLocation(ctx context.Context, driverID string, lat, lng float64) error {
	return m.UpdateLocationFn(ctx, driverID, lat, lng)
}
func (m *mockDriverService) UpdateStatus(ctx context.Context, driverID, status string) (*Driver, error) {
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
	h := NewHandler(mock)
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
		RegisterFn: func(_ context.Context, req RegisterRequest) (*AuthResponse, error) {
			return &AuthResponse{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				Driver:       &Driver{ID: "d1", Name: req.Name, Email: req.Email, Phone: req.Phone, Status: "available", Rating: 5.0},
			}, nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/register", RegisterRequest{
		Name: "Bob", Email: "bob@example.com", Phone: "+1234567890", Password: "secret123",
		VehicleType: "sedan", LicensePlate: "ABC-123",
	}, "")

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp AuthResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.AccessToken != "access-tok" {
		t.Fatalf("unexpected access token: %s", resp.AccessToken)
	}
}

func TestLogin_Success(t *testing.T) {
	mock := &mockDriverService{
		LoginFn: func(_ context.Context, req LoginRequest) (*AuthResponse, error) {
			return &AuthResponse{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				Driver:       &Driver{ID: "d1", Email: req.Email},
			}, nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/login", LoginRequest{
		Email: "bob@example.com", Password: "secret123",
	}, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRefresh_Success(t *testing.T) {
	mock := &mockDriverService{
		RefreshFn: func(_ context.Context, token string) (*RefreshResponse, error) {
			return &RefreshResponse{AccessToken: "new-access", RefreshToken: "new-refresh"}, nil
		},
	}
	router := driverRouter(mock)

	rec := doRequest(router, http.MethodPost, "/drivers/refresh", RefreshRequest{
		RefreshToken: "some-token",
	}, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp RefreshResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.AccessToken != "new-access" {
		t.Fatalf("unexpected access token: %s", resp.AccessToken)
	}
}

func TestUpdateStatus_RequiresDriverRole(t *testing.T) {
	mock := &mockDriverService{
		UpdateStatusFn: func(_ context.Context, id, status string) (*Driver, error) {
			return &Driver{ID: id, Status: status}, nil
		},
	}
	router := driverRouter(mock)

	// Generate a rider token -- should be forbidden for driver-only endpoint
	riderToken, err := jwt.Generate("u1", "rider@example.com", "rider")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodPatch, "/drivers/d1/status", StatusUpdate{
		Status: "available",
	}, riderToken)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
