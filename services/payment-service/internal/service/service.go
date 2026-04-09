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
	// InitPayment is called by the Kafka consumer on trip.completed.
	// Cash payments are immediately completed; card payments stay PENDING.
	InitPayment(ctx context.Context, tripID, riderID, riderEmail, driverID, paymentMethod string, amount float64) (*model.Payment, error)

	// CreateOrder creates a provider order and transitions the payment to PROCESSING.
	// Returns an OrderResponse the frontend uses to launch the checkout widget.
	CreateOrder(ctx context.Context, paymentID string) (*model.OrderResponse, error)

	// VerifyPayment verifies the frontend signature and completes the payment.
	VerifyPayment(ctx context.Context, req model.VerifyRequest) (*model.Payment, error)

	// HandleWebhook processes an inbound provider webhook as a backup confirmation path.
	HandleWebhook(ctx context.Context, body []byte, signature string) error

	// SimulateSuccess completes a payment without provider interaction (dev/testing only).
	SimulateSuccess(ctx context.Context, paymentID string) (*model.Payment, error)

	GetByPaymentID(ctx context.Context, id string) (*model.Payment, error)
	GetByTripID(ctx context.Context, tripID string) (*model.Payment, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) (*model.PaymentHistoryResponse, error)
}

type paymentService struct {
	repo     repositories.PaymentRepository
	kafka    *kafka.Client
	provider provider.PaymentProvider
	keyID    string // Razorpay publishable key, returned in OrderResponse
	hub      HubBroadcaster
	baseURL  string
}

// NewService returns a PaymentService wired to the given dependencies.
func NewService(repo repositories.PaymentRepository, k *kafka.Client, prov provider.PaymentProvider, keyID string, hub HubBroadcaster, baseURL string) PaymentService {
	return &paymentService{repo: repo, kafka: k, provider: prov, keyID: keyID, hub: hub, baseURL: baseURL}
}

// InitPayment is the Kafka consumer entry point.
func (s *paymentService) InitPayment(ctx context.Context, tripID, riderID, riderEmail, driverID, paymentMethod string, amount float64) (*model.Payment, error) {
	if amount <= 0 {
		return nil, errors.New("payment amount must be positive")
	}
	providerName := paymentMethod
	if providerName == "" {
		providerName = "cash"
		paymentMethod = "cash"
	}

	p, err := s.repo.Create(ctx, tripID, riderID, riderEmail, driverID, paymentMethod, providerName, amount)
	if err != nil {
		return nil, err
	}
	// ON CONFLICT returned nothing — fetch the existing record
	if p == nil {
		return s.repo.FindByTripID(ctx, tripID)
	}

	// Cash payments are completed immediately
	if paymentMethod == "cash" {
		now := time.Now()
		if err := s.repo.MarkCompleted(ctx, p.ID, "", "", now); err != nil {
			log.Printf("[payments] failed to complete cash payment %s: %v", p.ID, err)
			return p, nil
		}
		p.Status = model.StatusCompleted
		p.CompletedAt = &now
		s.publishCompleted(ctx, p)
		log.Printf("[payments] cash payment %s completed for trip %s", p.ID, tripID)
	}

	return p, nil
}

// CreateOrder creates a provider order and moves the payment to PROCESSING.
func (s *paymentService) CreateOrder(ctx context.Context, paymentID string) (*model.OrderResponse, error) {
	p, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}
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

	// Generate a 30-min checkout token so the HTML page can call authenticated endpoints
	checkoutToken, err := jwt.GenerateCheckoutToken(p.RiderID, p.RiderEmail, "rider")
	if err != nil {
		return nil, fmt.Errorf("generate checkout token: %w", err)
	}

	checkoutURL := fmt.Sprintf("%s/payments/checkout/%s?token=%s", s.baseURL, p.ID, checkoutToken)

	return &model.OrderResponse{
		PaymentID:       p.ID,
		ProviderOrderID: order.ProviderOrderID,
		Amount:          p.Amount,
		Currency:        "INR",
		KeyID:           s.keyID,
		CheckoutURL:     checkoutURL,
	}, nil
}

// VerifyPayment verifies the Razorpay signature from the frontend and completes the payment.
func (s *paymentService) VerifyPayment(ctx context.Context, req model.VerifyRequest) (*model.Payment, error) {
	p, err := s.repo.FindByID(ctx, req.PaymentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}
	if p.Status == model.StatusCompleted {
		return p, nil // webhook already completed this payment — idempotent
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

// HandleWebhook processes a provider webhook as a backup confirmation path.
func (s *paymentService) HandleWebhook(ctx context.Context, body []byte, signature string) error {
	result, err := s.provider.ParseWebhook(body, signature)
	if err != nil {
		return err
	}
	if result == nil {
		return nil // event we don't care about
	}

	p, err := s.repo.FindByProviderOrderID(ctx, result.ProviderOrderID)
	if err != nil {
		return fmt.Errorf("webhook: payment lookup: %w", err)
	}
	if p.Status == model.StatusCompleted {
		return nil // already completed — idempotent
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

// SimulateSuccess completes any PENDING or PROCESSING payment without provider interaction.
func (s *paymentService) SimulateSuccess(ctx context.Context, paymentID string) (*model.Payment, error) {
	p, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}
	if p.Status != model.StatusPending && p.Status != model.StatusProcessing {
		return nil, fmt.Errorf("payment is in status %s; must be PENDING or PROCESSING to simulate", p.Status)
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

// GetByPaymentID retrieves a payment by its internal UUID.
func (s *paymentService) GetByPaymentID(ctx context.Context, id string) (*model.Payment, error) {
	p, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, errors.New("payment not found")
	}
	return p, nil
}

// GetByTripID retrieves a payment by trip ID.
func (s *paymentService) GetByTripID(ctx context.Context, tripID string) (*model.Payment, error) {
	p, err := s.repo.FindByTripID(ctx, tripID)
	if err != nil {
		return nil, errors.New("payment not found")
	}
	return p, nil
}

// ListByUser returns paginated payments for a rider or driver.
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
