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
	InitPayment(ctx context.Context, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod string, amount float64) (*model.Payment, error)

	// CreateOrder creates a provider order and returns checkout details.
	// For cash: skips Razorpay, returns checkout_url with null provider_order_id.
	// For UPI:  creates order + QR code, returns upi_qr_url.
	// For card: creates order, returns provider_order_id + key_id.
	CreateOrder(ctx context.Context, paymentID string) (*model.OrderResponse, error)

	// VerifyPayment verifies the frontend signature and completes a card payment.
	VerifyPayment(ctx context.Context, req model.VerifyRequest) (*model.Payment, error)

	// HandleWebhook processes an inbound provider webhook as a backup confirmation path.
	HandleWebhook(ctx context.Context, body []byte, signature string) error

	// ConfirmCash is called by the driver to confirm cash was collected.
	ConfirmCash(ctx context.Context, paymentID, driverID string) (*model.Payment, error)

	// InitiateUPICollect sends a UPI collect request to the rider's VPA.
	InitiateUPICollect(ctx context.Context, paymentID, vpa string) error

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
func (s *paymentService) InitPayment(ctx context.Context, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod string, amount float64) (*model.Payment, error) {
	if amount <= 0 {
		return nil, errors.New("payment amount must be positive")
	}
	providerName := paymentMethod
	if providerName == "" {
		providerName = "cash"
		paymentMethod = "cash"
	}

	p, err := s.repo.Create(ctx, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod, providerName, amount)
	if err != nil {
		return nil, err
	}
	// ON CONFLICT returned nothing — fetch the existing record
	if p == nil {
		return s.repo.FindByTripID(ctx, tripID)
	}

	// Cash payments wait for driver confirmation
	if paymentMethod == "cash" {
		if err := s.repo.MarkAwaitingCashConfirm(ctx, p.ID); err != nil {
			log.Printf("[payments] failed to mark cash payment %s awaiting confirm: %v", p.ID, err)
			return p, nil
		}
		p.Status = model.StatusAwaitingCashConfirm
		log.Printf("[payments] cash payment %s awaiting driver confirmation for trip %s", p.ID, tripID)
	}

	return p, nil
}

// CreateOrder creates a provider order and returns checkout details.
func (s *paymentService) CreateOrder(ctx context.Context, paymentID string) (*model.OrderResponse, error) {
	p, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}

	// Generate a 30-min checkout token
	checkoutToken, err := jwt.GenerateCheckoutToken(p.RiderID, p.RiderEmail, "rider")
	if err != nil {
		return nil, fmt.Errorf("generate checkout token: %w", err)
	}
	checkoutURL := fmt.Sprintf("%s/payments/checkout/%s?token=%s", s.baseURL, p.ID, checkoutToken)

	switch p.PaymentMethod {
	case "cash":
		// Cash: no provider interaction; payment is already AWAITING_CASH_CONFIRM
		if p.Status != model.StatusAwaitingCashConfirm {
			return nil, fmt.Errorf("cash payment is in status %s, expected AWAITING_CASH_CONFIRM", p.Status)
		}
		return &model.OrderResponse{
			PaymentID:   p.ID,
			Amount:      p.Amount,
			Currency:    "INR",
			CheckoutURL: checkoutURL,
		}, nil

	case "upi":
		if p.Status != model.StatusPending {
			return nil, fmt.Errorf("payment is in status %s, expected PENDING", p.Status)
		}
		upiOrder, err := s.provider.CreateUPIOrder(ctx, p.Amount, "INR", p.TripID)
		if err != nil {
			return nil, fmt.Errorf("create UPI order: %w", err)
		}
		if err := s.repo.MarkProcessing(ctx, p.ID, upiOrder.ProviderOrderID, upiOrder.QRCodeID, upiOrder.QRImageURL); err != nil {
			return nil, fmt.Errorf("mark processing: %w", err)
		}
		resp := &model.OrderResponse{
			PaymentID:   p.ID,
			Amount:      p.Amount,
			Currency:    "INR",
			KeyID:       s.keyID,
			CheckoutURL: checkoutURL,
		}
		resp.ProviderOrderID = &upiOrder.ProviderOrderID
		if upiOrder.QRImageURL != "" {
			resp.UPIQRUrl = &upiOrder.QRImageURL
		}
		return resp, nil

	default: // card
		if p.Status != model.StatusPending {
			return nil, fmt.Errorf("payment is in status %s, expected PENDING", p.Status)
		}
		order, err := s.provider.CreateOrder(ctx, p.Amount, "INR", p.TripID)
		if err != nil {
			return nil, fmt.Errorf("create provider order: %w", err)
		}
		if err := s.repo.MarkProcessing(ctx, p.ID, order.ProviderOrderID, "", ""); err != nil {
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

	// Look up payment: by order ID (card/UPI VPA) or by QR ID (UPI QR scan)
	var p *model.Payment
	if result.ProviderOrderID != "" {
		p, err = s.repo.FindByProviderOrderID(ctx, result.ProviderOrderID)
	} else if result.ProviderQRID != "" {
		p, err = s.repo.FindByProviderQRID(ctx, result.ProviderQRID)
	} else {
		return nil
	}
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

// ConfirmCash is called by the driver after collecting cash from the rider.
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

// InitiateUPICollect sends a UPI collect request to the rider's VPA.
func (s *paymentService) InitiateUPICollect(ctx context.Context, paymentID, vpa string) error {
	p, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return fmt.Errorf("payment not found: %w", err)
	}
	if p.PaymentMethod != "upi" {
		return fmt.Errorf("not a UPI payment")
	}
	if p.Status != model.StatusProcessing {
		return fmt.Errorf("payment is in status %s, expected PROCESSING", p.Status)
	}
	return s.provider.InitiateUPICollect(ctx, p.ProviderOrderID, vpa, p.RiderPhone, p.RiderEmail)
}

// SimulateSuccess completes any non-terminal payment without provider interaction (dev/testing only).
func (s *paymentService) SimulateSuccess(ctx context.Context, paymentID string) (*model.Payment, error) {
	p, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return nil, fmt.Errorf("payment not found: %w", err)
	}
	if p.Status != model.StatusPending && p.Status != model.StatusProcessing && p.Status != model.StatusAwaitingCashConfirm {
		return nil, fmt.Errorf("payment is in status %s; must be PENDING, PROCESSING, or AWAITING_CASH_CONFIRM to simulate", p.Status)
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
