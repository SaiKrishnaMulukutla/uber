package service

import (
	"context"
	"errors"
	"log"
	"time"

	"uber/shared/pkg/kafka"
	"uber/payment-service/internal/model"
	"uber/payment-service/internal/repositories"
)

type PaymentService interface {
	CreatePayment(ctx context.Context, tripID, riderID, riderEmail, driverID string, amount float64) (*model.Payment, error)
	GetByTripID(ctx context.Context, tripID string) (*model.Payment, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) (*model.PaymentHistoryResponse, error)
}

type paymentService struct {
	repo  repositories.PaymentRepository
	kafka *kafka.Client
}

func NewService(repo repositories.PaymentRepository, k *kafka.Client) PaymentService {
	return &paymentService{repo: repo, kafka: k}
}

func (s *paymentService) CreatePayment(ctx context.Context, tripID, riderID, riderEmail, driverID string, amount float64) (*model.Payment, error) {
	if amount <= 0 {
		return nil, errors.New("payment amount must be positive")
	}
	p, err := s.repo.Create(ctx, tripID, riderID, driverID, amount)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if err := s.repo.MarkCompleted(ctx, p.ID, now); err != nil {
		log.Printf("[payments] failed to complete payment %s: %v", p.ID, err)
		return p, nil
	}
	p.Status = model.StatusCompleted
	p.CompletedAt = &now

	log.Printf("[payments] payment %s completed for trip %s", p.ID, tripID)
	ev := kafka.PaymentCompletedEvent{
		PaymentID:   p.ID,
		TripID:      tripID,
		RiderID:     riderID,
		RiderEmail:  riderEmail,
		DriverID:    driverID,
		Amount:      amount,
		CompletedAt: now.Format(time.RFC3339),
	}
	if err := s.kafka.Publish(ctx, kafka.TopicPaymentCompleted, tripID, ev); err != nil {
		log.Printf("[payments] failed to publish payment.completed: %v", err)
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
