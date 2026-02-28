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
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"` // "rider" or "driver"
	gojwt.RegisteredClaims
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

// Generate creates a signed JWT for the given user.
func Generate(userID, email, role string) (string, error) {
	claims := Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: gojwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  gojwt.NewNumericDate(time.Now()),
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
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

// ---- HTTP Middleware ----

// OptionalAuth extracts JWT claims into context if a Bearer token is present.
// Requests without a token pass through (claims will be nil).
func OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			if claims, err := Validate(auth[7:]); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), claimsCtxKey, claims))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth rejects requests that have no valid JWT in context.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetClaims(r.Context()) == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetClaims retrieves the parsed claims from context (nil if absent).
func GetClaims(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsCtxKey).(*Claims)
	return c
}
