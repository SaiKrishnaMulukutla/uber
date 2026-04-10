package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"uber/payment-service/internal/hub"
	"uber/payment-service/internal/model"
	"uber/shared/pkg/jwt"
)

// PaymentServicer is the subset of service.PaymentService the handler needs.
type PaymentServicer interface {
	CreateOrder(ctx context.Context, paymentID string) (*model.OrderResponse, error)
	VerifyPayment(ctx context.Context, req model.VerifyRequest) (*model.Payment, error)
	HandleWebhook(ctx context.Context, body []byte, signature string) error
	ConfirmCash(ctx context.Context, paymentID, driverID string) (*model.Payment, error)
	SimulateSuccess(ctx context.Context, paymentID string) (*model.Payment, error)
	GetByPaymentID(ctx context.Context, id string) (*model.Payment, error)
	GetByTripID(ctx context.Context, tripID string) (*model.Payment, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) (*model.PaymentHistoryResponse, error)
}

type Handler struct {
	svc   PaymentServicer
	hub   *hub.PaymentHub
	keyID string
}

func NewHandler(svc PaymentServicer, h *hub.PaymentHub, keyID string) *Handler {
	return &Handler{svc: svc, hub: h, keyID: keyID}
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	r.Post("/webhook", h.Webhook)
	r.Get("/checkout/{id}", h.Checkout)
	r.Get("/ws/{id}", h.CheckoutWS)

	r.Group(func(r chi.Router) {
		r.Use(jwt.RequireCheckoutAuth)
		r.Post("/verify", h.VerifyPayment)
	})

	r.Group(func(r chi.Router) {
		r.Use(jwt.RequireAuth)
		r.Use(jwt.RequireRole("rider", "driver"))
		r.Get("/history", h.History)
		r.Get("/{tripId}", h.GetByTripID)
		r.Post("/orders", h.CreateOrder)
		r.Post("/simulate-success", h.Simulate)
		r.Post("/{id}/confirm-cash", h.ConfirmCash)
	})

	return r
}

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	limit, offset := parsePagination(r)
	resp, err := h.svc.ListByUser(r.Context(), claims.UserID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetByTripID(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	tripID := chi.URLParam(r, "tripId")
	p, err := h.svc.GetByTripID(r.Context(), tripID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if claims.UserID != p.RiderID && claims.UserID != p.DriverID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not your payment"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "rider" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only riders can create orders"})
		return
	}
	var body struct {
		PaymentID string `json:"payment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PaymentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payment_id is required"})
		return
	}
	resp, err := h.svc.CreateOrder(r.Context(), body.PaymentID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) VerifyPayment(w http.ResponseWriter, r *http.Request) {
	var req model.VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.PaymentID == "" || req.ProviderOrderID == "" || req.ProviderPaymentID == "" || req.Signature == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payment_id, provider_order_id, provider_payment_id, and signature are required"})
		return
	}
	p, err := h.svc.VerifyPayment(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	sig := r.Header.Get("X-Razorpay-Signature")
	if err := h.svc.HandleWebhook(r.Context(), body, sig); err != nil {
		_ = err
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) Simulate(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	var body struct {
		PaymentID string `json:"payment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PaymentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payment_id is required"})
		return
	}
	existing, err := h.svc.GetByPaymentID(r.Context(), body.PaymentID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	if claims.UserID != existing.RiderID && claims.UserID != existing.DriverID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not your payment"})
		return
	}
	p, err := h.svc.SimulateSuccess(r.Context(), body.PaymentID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) ConfirmCash(w http.ResponseWriter, r *http.Request) {
	claims := jwt.GetClaims(r.Context())
	if claims.Role != "driver" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only drivers can confirm cash payments"})
		return
	}
	paymentID := chi.URLParam(r, "id")
	p, err := h.svc.ConfirmCash(r.Context(), paymentID, claims.UserID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": p.Status, "amount": p.Amount})
}

func parsePagination(r *http.Request) (limit, offset int) {
	limit = 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 {
			limit = l
		}
	}
	if limit > 50 {
		limit = 50
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if o, err := strconv.Atoi(v); err == nil && o >= 0 {
			offset = o
		}
	}
	if offset > 10000 {
		offset = 10000
	}
	return
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
