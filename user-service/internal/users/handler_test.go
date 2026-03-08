package users

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

type mockUserService struct {
	RegisterFn func(ctx context.Context, req RegisterRequest) (*AuthResponse, error)
	LoginFn    func(ctx context.Context, req LoginRequest) (*AuthResponse, error)
	RefreshFn  func(ctx context.Context, refreshToken string) (*RefreshResponse, error)
	GetByIDFn  func(ctx context.Context, id string) (*User, error)
}

func (m *mockUserService) Register(ctx context.Context, req RegisterRequest) (*AuthResponse, error) {
	return m.RegisterFn(ctx, req)
}
func (m *mockUserService) Login(ctx context.Context, req LoginRequest) (*AuthResponse, error) {
	return m.LoginFn(ctx, req)
}
func (m *mockUserService) Refresh(ctx context.Context, refreshToken string) (*RefreshResponse, error) {
	return m.RefreshFn(ctx, refreshToken)
}
func (m *mockUserService) GetByID(ctx context.Context, id string) (*User, error) {
	return m.GetByIDFn(ctx, id)
}

// ---------- helpers ----------

func TestMain(m *testing.M) {
	jwt.Init("test-secret")
	os.Exit(m.Run())
}

func userRouter(mock *mockUserService) http.Handler {
	h := NewHandler(mock)
	r := chi.NewRouter()
	r.Use(jwt.OptionalAuth)
	r.Mount("/users", h.Routes())
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
	mock := &mockUserService{
		RegisterFn: func(_ context.Context, req RegisterRequest) (*AuthResponse, error) {
			return &AuthResponse{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				User:         &User{ID: "u1", Name: req.Name, Email: req.Email, Phone: req.Phone, Rating: 5.0},
			}, nil
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/register", RegisterRequest{
		Name: "Alice", Email: "alice@example.com", Phone: "+1234567890", Password: "secret123",
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
	mock := &mockUserService{
		LoginFn: func(_ context.Context, req LoginRequest) (*AuthResponse, error) {
			return &AuthResponse{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				User:         &User{ID: "u1", Email: req.Email},
			}, nil
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/login", LoginRequest{
		Email: "alice@example.com", Password: "secret123",
	}, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRefresh_Success(t *testing.T) {
	mock := &mockUserService{
		RefreshFn: func(_ context.Context, token string) (*RefreshResponse, error) {
			return &RefreshResponse{AccessToken: "new-access", RefreshToken: "new-refresh"}, nil
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/refresh", RefreshRequest{
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

func TestRefresh_InvalidToken(t *testing.T) {
	mock := &mockUserService{
		RefreshFn: func(_ context.Context, token string) (*RefreshResponse, error) {
			return nil, nil // should not be called
		},
	}
	router := userRouter(mock)

	// Send empty refresh_token
	rec := doRequest(router, http.MethodPost, "/users/refresh", map[string]string{
		"refresh_token": "",
	}, "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetProfile_RequiresRiderRole(t *testing.T) {
	mock := &mockUserService{
		GetByIDFn: func(_ context.Context, id string) (*User, error) {
			return &User{ID: id, Name: "Alice"}, nil
		},
	}
	router := userRouter(mock)

	// Generate a driver token -- should be forbidden for rider-only endpoint
	driverToken, err := jwt.Generate("d1", "driver@example.com", "driver")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodGet, "/users/u1", nil, driverToken)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
