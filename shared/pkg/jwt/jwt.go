package jwt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

// Claims represents the JWT payload.
type Claims struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	Phone     string `json:"phone,omitempty"`
	Role      string `json:"role"`       // "rider" or "driver"
	TokenType string `json:"token_type"` // "access" or "refresh"
	gojwt.RegisteredClaims
}

// TokenPair holds an access token and a refresh token.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type ctxKey string

const claimsCtxKey ctxKey = "jwt_claims"

var secret []byte

// Init must be called once at startup with the JWT_SECRET value.
func Init(s string) error {
	if s == "" {
		return errors.New("JWT_SECRET is required")
	}
	secret = []byte(s)
	return nil
}

// Generate creates a signed access JWT (15 min) for the given user.
func Generate(userID, email, role string) (string, error) {
	return generateToken(userID, email, "", role, "access", 15*time.Minute)
}

// GenerateCheckoutToken creates a 30-minute token for the payment checkout page.
func GenerateCheckoutToken(userID, email, role string) (string, error) {
	return generateToken(userID, email, "", role, "checkout", 30*time.Minute)
}

// GenerateTokenPair returns a short-lived access token and a long-lived refresh token.
// Pass phone="" if not available (e.g. driver auth).
func GenerateTokenPair(userID, email, phone, role string) (*TokenPair, error) {
	access, err := generateToken(userID, email, phone, role, "access", 15*time.Minute)
	if err != nil {
		return nil, err
	}
	refresh, err := generateToken(userID, email, phone, role, "refresh", 7*24*time.Hour)
	if err != nil {
		return nil, err
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh}, nil
}

func generateToken(userID, email, phone, role, tokenType string, duration time.Duration) (string, error) {
	claims := Claims{
		UserID:    userID,
		Email:     email,
		Phone:     phone,
		Role:      role,
		TokenType: tokenType,
		RegisteredClaims: gojwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  gojwt.NewNumericDate(time.Now()),
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(duration)),
		},
	}
	return gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString(secret)
}

// Validate parses and validates a raw JWT string.
func Validate(raw string) (*Claims, error) {
	token, err := gojwt.ParseWithClaims(raw, &Claims{}, func(t *gojwt.Token) (any, error) {
		if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// ValidateRefreshToken validates a raw JWT and ensures it is a refresh token.
func ValidateRefreshToken(raw string) (*Claims, error) {
	claims, err := Validate(raw)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != "refresh" {
		return nil, errors.New("not a refresh token")
	}
	return claims, nil
}

// ---- HTTP Middleware ----

// OptionalAuth extracts JWT claims into context if a Bearer token is present.
// Also accepts ?token= query param (used by checkout pages).
// Requests without a token pass through (claims will be nil).
func OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := ""
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			raw = auth[7:]
		} else if t := r.URL.Query().Get("token"); t != "" {
			raw = t
		}
		if raw != "" {
			if claims, err := Validate(raw); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), claimsCtxKey, claims))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth rejects requests that have no valid JWT in context or that use a refresh token.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil || claims.TokenType == "refresh" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireCheckoutAuth allows access tokens and checkout tokens (but not refresh tokens).
func RequireCheckoutAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil || claims.TokenType == "refresh" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole rejects requests whose JWT Role claim is not in the allowed set.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if !allowed[claims.Role] {
				http.Error(w, `{"error":"forbidden: insufficient role"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetClaims retrieves the parsed claims from context (nil if absent).
func GetClaims(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsCtxKey).(*Claims)
	return c
}
