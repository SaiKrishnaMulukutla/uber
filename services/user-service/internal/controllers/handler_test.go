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

	"uber/shared/pkg/jwt"
	"uber/shared/pkg/otp"
	"uber/user-service/internal/model"
)

// ---------- mock ----------

type mockUserService struct {
	RegisterFn       func(ctx context.Context, req model.RegisterRequest) error
	VerifyRegisterFn func(ctx context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error)
	LoginFn          func(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error)
	RefreshFn        func(ctx context.Context, refreshToken string) (*model.RefreshResponse, error)
	GetByIDFn        func(ctx context.Context, id string) (*model.User, error)
	UpdateFn         func(ctx context.Context, id string, req model.UpdateRequest) (*model.User, error)
}

func (m *mockUserService) Register(ctx context.Context, req model.RegisterRequest) error {
	return m.RegisterFn(ctx, req)
}
func (m *mockUserService) VerifyRegister(ctx context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
	return m.VerifyRegisterFn(ctx, req)
}
func (m *mockUserService) Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
	return m.LoginFn(ctx, req)
}
func (m *mockUserService) Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error) {
	return m.RefreshFn(ctx, refreshToken)
}
func (m *mockUserService) GetByID(ctx context.Context, id string) (*model.User, error) {
	return m.GetByIDFn(ctx, id)
}
func (m *mockUserService) Update(ctx context.Context, id string, req model.UpdateRequest) (*model.User, error) {
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
		RegisterFn: func(_ context.Context, req model.RegisterRequest) error {
			return nil
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/register", model.RegisterRequest{
		Name: "Alice", Email: "alice@example.com", Phone: "+1234567890", Password: "secret123",
	}, "")

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVerifyRegister_Success(t *testing.T) {
	mock := &mockUserService{
		VerifyRegisterFn: func(_ context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
			return &model.AuthResponse{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				User:         &model.User{ID: "u1", Email: req.Email},
			}, nil
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/verify-register", model.VerifyRegisterRequest{
		Email: "alice@example.com", OTP: "123456",
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
	mock := &mockUserService{
		VerifyRegisterFn: func(_ context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
			return nil, otp.ErrInvalidOTP
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/verify-register", model.VerifyRegisterRequest{
		Email: "alice@example.com", OTP: "000000",
	}, "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVerifyRegister_MaxAttempts(t *testing.T) {
	mock := &mockUserService{
		VerifyRegisterFn: func(_ context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error) {
			return nil, otp.ErrMaxAttemptsExceeded
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/verify-register", model.VerifyRegisterRequest{
		Email: "alice@example.com", OTP: "000000",
	}, "")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLogin_Success(t *testing.T) {
	mock := &mockUserService{
		LoginFn: func(_ context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
			return &model.AuthResponse{AccessToken: "access-tok", RefreshToken: "refresh-tok"}, nil
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/login", model.LoginRequest{
		Email: "alice@example.com", Password: "secret123",
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
	mock := &mockUserService{
		LoginFn: func(_ context.Context, req model.LoginRequest) (*model.AuthResponse, error) {
			return nil, errors.New("invalid credentials")
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/login", model.LoginRequest{
		Email: "alice@example.com", Password: "wrongpassword",
	}, "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRefresh_Success(t *testing.T) {
	mock := &mockUserService{
		RefreshFn: func(_ context.Context, token string) (*model.RefreshResponse, error) {
			return &model.RefreshResponse{AccessToken: "new-access", RefreshToken: "new-refresh"}, nil
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/refresh", model.RefreshRequest{
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

func TestRefresh_InvalidToken(t *testing.T) {
	mock := &mockUserService{
		RefreshFn: func(_ context.Context, token string) (*model.RefreshResponse, error) {
			return nil, nil
		},
	}
	router := userRouter(mock)

	rec := doRequest(router, http.MethodPost, "/users/refresh", map[string]string{
		"refresh_token": "",
	}, "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetProfile_RequiresRiderRole(t *testing.T) {
	mock := &mockUserService{
		GetByIDFn: func(_ context.Context, id string) (*model.User, error) {
			return &model.User{ID: id, Name: "Alice"}, nil
		},
	}
	router := userRouter(mock)

	driverToken, err := jwt.Generate("d1", "driver@example.com", "driver")
	if err != nil {
		t.Fatal(err)
	}

	rec := doRequest(router, http.MethodGet, "/users/u1", nil, driverToken)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
