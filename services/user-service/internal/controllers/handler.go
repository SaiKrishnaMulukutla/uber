package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"uber/shared/pkg/httputil"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/otp"
	"uber/shared/pkg/validation"
	"uber/user-service/internal/model"
)

var sixDigits = regexp.MustCompile(`^\d{6}$`)

type UserServicer interface {
	Register(ctx context.Context, req model.RegisterRequest) error
	VerifyRegister(ctx context.Context, req model.VerifyRegisterRequest) (*model.AuthResponse, error)
	Login(ctx context.Context, req model.LoginRequest) (*model.AuthResponse, error)
	Refresh(ctx context.Context, refreshToken string) (*model.RefreshResponse, error)
	GetByID(ctx context.Context, id string) (*model.User, error)
	Update(ctx context.Context, id string, req model.UpdateRequest) (*model.User, error)
	ForgotPassword(ctx context.Context, req model.ForgotPasswordRequest) error
	ResetPassword(ctx context.Context, req model.ResetPasswordRequest) error
}

type Handler struct{ svc UserServicer }

func NewHandler(svc UserServicer) *Handler { return &Handler{svc: svc} }

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	r.Post("/register", h.Register)
	r.Post("/verify-register", h.VerifyRegister)
	r.Post("/login", h.Login)
	r.Post("/refresh", h.Refresh)
	r.Post("/forgot-password", h.ForgotPassword)
	r.Post("/reset-password", h.ResetPassword)

	r.Group(func(r chi.Router) {
		r.Use(jwt.RequireAuth)
		r.Use(jwt.RequireRole("rider"))
		r.Get("/{id}", h.GetProfile)
		r.Patch("/{id}", h.UpdateProfile)
	})

	return r
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req model.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateName(req.Name) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return
	}
	if !validation.ValidateEmail(req.Email) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if !validation.ValidatePhone(req.Phone) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid phone"})
		return
	}
	if !validation.ValidatePassword(req.Password) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 6 characters"})
		return
	}
	if err := h.svc.Register(r.Context(), req); err != nil {
		if errors.Is(err, otp.ErrRateLimitExceeded) {
			httputil.WriteJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusAccepted, map[string]string{"message": "OTP sent to " + req.Email})
}

// VerifyRegister confirms the OTP and creates the account, returning a JWT on success.
func (h *Handler) VerifyRegister(w http.ResponseWriter, r *http.Request) {
	var req model.VerifyRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateEmail(req.Email) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if !sixDigits.MatchString(req.OTP) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "otp must be exactly 6 digits"})
		return
	}
	resp, err := h.svc.VerifyRegister(r.Context(), req)
	if err != nil {
		if errors.Is(err, otp.ErrMaxAttemptsExceeded) {
			httputil.WriteJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, resp)
}

// Login validates credentials and returns a JWT directly.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req model.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateEmail(req.Email) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if !validation.ValidatePassword(req.Password) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 6 characters"})
		return
	}
	resp, err := h.svc.Login(r.Context(), req)
	if err != nil {
		httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req model.RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "refresh_token is required"})
		return
	}
	resp, err := h.svc.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := jwt.GetClaims(r.Context())
	if claims == nil || claims.UserID != id {
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	var req model.UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Name != "" && !validation.ValidateName(req.Name) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return
	}
	if req.Phone != "" && !validation.ValidatePhone(req.Phone) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid phone"})
		return
	}
	u, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, u)
}

func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	// Check ownership before hitting the DB to prevent user ID enumeration
	// via distinguishable 404 vs 403 responses.
	claims := jwt.GetClaims(r.Context())
	if claims == nil || claims.UserID != id {
		httputil.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	u, err := h.svc.GetByID(r.Context(), id)
	if err != nil {
		httputil.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, u)
}

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req model.ForgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateEmail(req.Email) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if err := h.svc.ForgotPassword(r.Context(), req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusAccepted, map[string]string{"message": "If an account with that email exists, an OTP has been sent."})
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req model.ResetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validation.ValidateEmail(req.Email) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if !sixDigits.MatchString(req.OTP) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "otp must be exactly 6 digits"})
		return
	}
	if !validation.ValidatePassword(req.NewPassword) {
		httputil.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 6 characters"})
		return
	}
	if err := h.svc.ResetPassword(r.Context(), req); err != nil {
		if errors.Is(err, otp.ErrMaxAttemptsExceeded) {
			httputil.WriteJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
			return
		}
		httputil.WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"message": "password reset successfully"})
}

