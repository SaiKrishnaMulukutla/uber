package jwt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if err := Init("test-secret"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// ---------- TestGenerateTokenPair ----------

func TestGenerateTokenPair(t *testing.T) {
	pair, err := GenerateTokenPair("u1", "u1@example.com", "", "rider")
	if err != nil {
		t.Fatalf("GenerateTokenPair returned error: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("access token is empty")
	}
	if pair.RefreshToken == "" {
		t.Fatal("refresh token is empty")
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Fatal("access and refresh tokens must be distinct")
	}

	// Validate access token claims
	claims, err := Validate(pair.AccessToken)
	if err != nil {
		t.Fatalf("Validate access token: %v", err)
	}
	if claims.UserID != "u1" {
		t.Errorf("UserID = %q, want %q", claims.UserID, "u1")
	}
	if claims.Email != "u1@example.com" {
		t.Errorf("Email = %q, want %q", claims.Email, "u1@example.com")
	}
	if claims.Role != "rider" {
		t.Errorf("Role = %q, want %q", claims.Role, "rider")
	}
	if claims.TokenType != "access" {
		t.Errorf("TokenType = %q, want %q", claims.TokenType, "access")
	}
}

// ---------- TestValidate_AccessToken ----------

func TestValidate_AccessToken(t *testing.T) {
	tok, err := Generate("u2", "u2@example.com", "driver")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	claims, err := Validate(tok)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"UserID", claims.UserID, "u2"},
		{"Email", claims.Email, "u2@example.com"},
		{"Role", claims.Role, "driver"},
		{"TokenType", claims.TokenType, "access"},
		{"Subject", claims.Subject, "u2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

// ---------- TestValidateRefreshToken ----------

func TestValidateRefreshToken(t *testing.T) {
	pair, err := GenerateTokenPair("u3", "u3@example.com", "", "rider")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"refresh token accepted", pair.RefreshToken, false},
		{"access token rejected", pair.AccessToken, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := ValidateRefreshToken(tc.token)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if claims.TokenType != "refresh" {
					t.Errorf("TokenType = %q, want %q", claims.TokenType, "refresh")
				}
				if claims.UserID != "u3" {
					t.Errorf("UserID = %q, want %q", claims.UserID, "u3")
				}
			}
		})
	}
}

// ---------- TestValidate_Expired ----------

func TestValidate_Expired(t *testing.T) {
	// Generate a token with a very short expiry using the internal helper.
	tok, err := generateToken("u4", "u4@example.com", "", "rider", "access", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}

	// Wait for the token to expire.
	time.Sleep(50 * time.Millisecond)

	_, err = Validate(tok)
	if err == nil {
		t.Error("expected error for expired token, got nil")
	}
}

// ---------- Middleware helpers ----------

// dummyHandler is a simple handler that writes 200 OK.
var dummyHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
})

// buildRequest creates a request with an access-token-bearing Authorization header
// and runs it through OptionalAuth so claims are in context.
func buildRequest(t *testing.T, token string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// Run through OptionalAuth to populate context.
	rec := httptest.NewRecorder()
	var captured *http.Request
	OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
	})).ServeHTTP(rec, req)
	return captured
}

// ---------- TestRequireRole_Allowed ----------

func TestRequireRole_Allowed(t *testing.T) {
	pair, err := GenerateTokenPair("u5", "u5@example.com", "", "rider")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	req := buildRequest(t, pair.AccessToken)
	rec := httptest.NewRecorder()

	middleware := RequireRole("rider", "admin")
	middleware(dummyHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// ---------- TestRequireRole_Forbidden ----------

func TestRequireRole_Forbidden(t *testing.T) {
	pair, err := GenerateTokenPair("u6", "u6@example.com", "", "driver")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	req := buildRequest(t, pair.AccessToken)
	rec := httptest.NewRecorder()

	middleware := RequireRole("rider") // driver is NOT in the allowed set
	middleware(dummyHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// ---------- TestRequireAuth_RejectsRefreshToken ----------

func TestRequireAuth_RejectsRefreshToken(t *testing.T) {
	pair, err := GenerateTokenPair("u7", "u7@example.com", "", "rider")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	// Manually inject refresh-token claims into context (OptionalAuth would also
	// populate it, but we want to be explicit).
	req := buildRequest(t, pair.RefreshToken)
	rec := httptest.NewRecorder()

	RequireAuth(dummyHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ---------- Extra: verify RequireAuth passes valid access token ----------

func TestRequireAuth_AcceptsAccessToken(t *testing.T) {
	pair, err := GenerateTokenPair("u8", "u8@example.com", "", "rider")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}

	req := buildRequest(t, pair.AccessToken)
	rec := httptest.NewRecorder()

	RequireAuth(dummyHandler).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// ---------- Extra: verify GetClaims returns nil for empty context ----------

func TestGetClaims_NilForEmptyContext(t *testing.T) {
	c := GetClaims(context.Background())
	if c != nil {
		t.Errorf("expected nil claims, got %+v", c)
	}
}
