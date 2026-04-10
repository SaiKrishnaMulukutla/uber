package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"uber/payment-service/internal/model"
	"uber/payment-service/internal/provider"
	"uber/payment-service/internal/repositories"
	"uber/shared/pkg/jwt"
	"uber/shared/pkg/kafka"
)

// HubBroadcaster is implemented by hub.PaymentHub.
type HubBroadcaster interface {
	Broadcast(paymentID string)
}

// PaymentService defines all payment operations.
type PaymentService interface {
	InitPayment(ctx context.Context, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod string, amount float64) (*model.Payment, error)
	CreateOrder(ctx context.Context, paymentID string) (*model.OrderResponse, error)
	VerifyPayment(ctx context.Context, req model.VerifyRequest) (*model.Payment, error)
	HandleWebhook(ctx context.Context, body []byte, signature string) error
	ConfirmCash(ctx context.Context, paymentID, driverID string) (*model.Payment, error)
	SimulateSuccess(ctx context.Context, paymentID string) (*model.Payment, error)
	GetByPaymentID(ctx context.Context, id string) (*model.Payment, error)
	GetByTripID(ctx context.Context, tripID string) (*model.Payment, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) (*model.PaymentHistoryResponse, error)
}

type paymentService struct {
	repo     repositories.PaymentRepository
	kafka    *kafka.Client
	provider provider.PaymentProvider
	keyID    string
	hub      HubBroadcaster
	baseURL  string
}

func NewService(repo repositories.PaymentRepository, k *kafka.Client, prov provider.PaymentProvider, keyID string, hub HubBroadcaster, baseURL string) PaymentService {
	return &paymentService{repo: repo, kafka: k, provider: prov, keyID: keyID, hub: hub, baseURL: baseURL}
}

func (s *paymentService) InitPayment(ctx context.Context, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod string, amount float64) (*model.Payment, error) {
	if amount <= 0 {
		return nil, errors.New("payment amount must be positive")
	}
	if paymentMethod == "" {
		paymentMethod = "cash"
	}
	p, err := s.repo.Create(ctx, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod, paymentMethod, amount)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return s.repo.FindByTripID(ctx, tripID)
	}
	if paymentMethod == "cash" {
		if err := s.repo.MarkAwaitingCashConfirm(ctx, p.ID); err != nil {
			log.Printf("[payments] failed to mark cash payment %s awaiting confirm: %v", p.ID, err)
			return p, nil
		}
		p.Status = model.StatusAwaitingCashConfirm
	}
	return p, nil
}

func (s *paymentService) CreateOrder(ctx context.Context, paymentID string) (*model.OrderResponse, error) {
	p, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}
	checkoutToken, err := jwt.GenerateCheckoutToken(p.RiderID, p.RiderEmail, "rider")
	if err != nil {
		return nil, fmt.Errorf("generate checkout token: %w", err)
	}
	checkoutURL := fmt.Sprintf("%s/payments/checkout/%s?token=%s", s.baseURL, p.ID, checkoutToken)

	if p.PaymentMethod == "cash" {
		if p.Status != model.StatusAwaitingCashConfirm {
			return nil, fmt.Errorf("cash payment is in status %s, expected AWAITING_CASH_CONFIRM", p.Status)
		}
		return &model.OrderResponse{
			PaymentID:   p.ID,
			Amount:      p.Amount,
			Currency:    "INR",
			CheckoutURL: checkoutURL,
		}, nil
	}

	// card or upi — both use Razorpay modal
	if p.Status != model.StatusPending {
		return nil, fmt.Errorf("payment is in status %s, expected PENDING", p.Status)
	}
	order, err := s.provider.CreateOrder(ctx, p.Amount, "INR", p.TripID)
	if err != nil {
		return nil, fmt.Errorf("create provider order: %w", err)
	}
	if err := s.repo.MarkProcessing(ctx, p.ID, order.ProviderOrderID); err != nil {
		return nil, fmt.Errorf("mark processing: %w", err)
	}
	return &model.OrderResponse{
		PaymentID:       p.ID,
		ProviderOrderID: &order.ProviderOrderID,
		Amount:          p.Amount,
		Currency:        "INR",
		KeyID:           s.keyID,
		CheckoutURL:     checkoutURL,
	}, nil
}

func (s *paymentService) VerifyPayment(ctx context.Context, req model.VerifyRequest) (*model.Payment, error) {
	p, err := s.repo.FindByID(ctx, req.PaymentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}
	if p.Status == model.StatusCompleted {
		return p, nil
	}
	if p.Status != model.StatusProcessing {
		return nil, fmt.Errorf("payment is in status %s, expected PROCESSING", p.Status)
	}
	if err := s.provider.VerifyPayment(ctx, req.ProviderOrderID, req.ProviderPaymentID, req.Signature); err != nil {
		_ = s.repo.MarkFailed(ctx, p.ID, err.Error())
		return nil, err
	}
	now := time.Now()
	if err := s.repo.MarkCompleted(ctx, p.ID, req.ProviderPaymentID, req.Signature, now); err != nil {
		return nil, fmt.Errorf("mark completed: %w", err)
	}
	p.Status = model.StatusCompleted
	p.ProviderPaymentID = req.ProviderPaymentID
	p.CompletedAt = &now
	s.publishCompleted(ctx, p)
	return p, nil
}

func (s *paymentService) HandleWebhook(ctx context.Context, body []byte, signature string) error {
	result, err := s.provider.ParseWebhook(body, signature)
	if err != nil {
		return err
	}
	if result == nil || result.ProviderOrderID == "" {
		return nil
	}
	p, err := s.repo.FindByProviderOrderID(ctx, result.ProviderOrderID)
	if err != nil {
		return fmt.Errorf("webhook: payment lookup: %w", err)
	}
	if p.Status == model.StatusCompleted {
		return nil
	}
	now := time.Now()
	if err := s.repo.MarkCompleted(ctx, p.ID, result.ProviderPaymentID, "", now); err != nil {
		return fmt.Errorf("webhook: mark completed: %w", err)
	}
	p.Status = model.StatusCompleted
	p.ProviderPaymentID = result.ProviderPaymentID
	p.CompletedAt = &now
	s.publishCompleted(ctx, p)
	return nil
}

func (s *paymentService) ConfirmCash(ctx context.Context, paymentID, driverID string) (*model.Payment, error) {
	p, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}
	if p.DriverID != driverID {
		return nil, fmt.Errorf("not your payment")
	}
	if p.Status != model.StatusAwaitingCashConfirm {
		return nil, fmt.Errorf("payment is in status %s; expected AWAITING_CASH_CONFIRM", p.Status)
	}
	now := time.Now()
	if err := s.repo.MarkCompleted(ctx, p.ID, "cash", "", now); err != nil {
		return nil, fmt.Errorf("confirm cash: mark completed: %w", err)
	}
	p.Status = model.StatusCompleted
	p.CompletedAt = &now
	s.publishCompleted(ctx, p)
	log.Printf("[payments] cash payment %s confirmed by driver %s", p.ID, driverID)
	return p, nil
}

func (s *paymentService) SimulateSuccess(ctx context.Context, paymentID string) (*model.Payment, error) {
	p, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}
	if p.Status != model.StatusPending && p.Status != model.StatusProcessing && p.Status != model.StatusAwaitingCashConfirm {
		return nil, fmt.Errorf("payment is in status %s; must be PENDING, PROCESSING, or AWAITING_CASH_CONFIRM", p.Status)
	}
	now := time.Now()
	if err := s.repo.MarkCompleted(ctx, p.ID, "simulated", "simulated", now); err != nil {
		return nil, fmt.Errorf("simulate: mark completed: %w", err)
	}
	p.Status = model.StatusCompleted
	p.CompletedAt = &now
	s.publishCompleted(ctx, p)
	return p, nil
}

func (s *paymentService) GetByPaymentID(ctx context.Context, id string) (*model.Payment, error) {
	p, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, errors.New("payment not found")
	}
	return p, nil
}

func (s *paymentService) GetByTripID(ctx context.Context, tripID string) (*model.Payment, error) {
	p, err := s.repo.FindByTripID(ctx, tripID)
	if err != nil {
		return nil, errors.New("payment not found")
	}
	return p, nil
}

func (s *paymentService) ListByUser(ctx context.Context, userID string, limit, offset int) (*model.PaymentHistoryResponse, error) {
	payments, total, err := s.repo.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	return &model.PaymentHistoryResponse{Payments: payments, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *paymentService) publishCompleted(ctx context.Context, p *model.Payment) {
	if s.hub != nil {
		s.hub.Broadcast(p.ID)
	}
	ev := kafka.PaymentCompletedEvent{
		PaymentID:   p.ID,
		TripID:      p.TripID,
		RiderID:     p.RiderID,
		RiderEmail:  p.RiderEmail,
		DriverID:    p.DriverID,
		Amount:      p.Amount,
		Status:      "COMPLETED",
		CompletedAt: p.CompletedAt.Format(time.RFC3339),
	}
	if err := s.kafka.Publish(ctx, kafka.TopicPaymentCompleted, p.TripID, ev); err != nil {
		log.Printf("[payments] failed to publish payment.completed: %v", err)
	}
}
