package payments

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"uber/shared/events"
	"uber/shared/pkg/kafka"
)

// Service contains payment business logic.
type Service struct {
	db    *pgxpool.Pool
	kafka *kafka.Client
}

// NewService creates a payment service.
func NewService(db *pgxpool.Pool, k *kafka.Client) *Service {
	return &Service{db: db, kafka: k}
}

// CreatePayment inserts a new payment and simulates async processing.
func (s *Service) CreatePayment(ctx context.Context, tripID, riderID, driverID string, amount float64) (*Payment, error) {
	if amount <= 0 {
		return nil, errors.New("payment amount must be positive")
	}
	var p Payment
	err := s.db.QueryRow(ctx,
		`INSERT INTO payments (trip_id, rider_id, driver_id, amount, status, payment_method)
		 VALUES ($1,$2,$3,$4,$5,'cash')
		 ON CONFLICT (trip_id) DO NOTHING
		 RETURNING id, trip_id, rider_id, driver_id, amount, status, payment_method, created_at, completed_at`,
		tripID, riderID, driverID, amount, StatusPending).
		Scan(&p.ID, &p.TripID, &p.RiderID, &p.DriverID, &p.Amount, &p.Status, &p.PaymentMethod, &p.CreatedAt, &p.CompletedAt)
	if err != nil {
		// ON CONFLICT DO NOTHING returns no rows — payment already exists
		existing, getErr := s.GetByTripID(ctx, tripID)
		if getErr == nil {
			log.Printf("[payments] payment already exists for trip %s, skipping", tripID)
			return existing, nil
		}
		return nil, err
	}

	// Simulate async payment processing
	go func() {
		time.Sleep(1 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		now := time.Now()
		_, err := s.db.Exec(ctx,
			`UPDATE payments SET status=$1, completed_at=$2 WHERE id=$3`,
			StatusCompleted, now, p.ID)
		if err != nil {
			log.Printf("[payments] failed to complete payment %s: %v", p.ID, err)
			return
		}
		log.Printf("[payments] payment %s completed for trip %s", p.ID, tripID)

		ev := events.PaymentCompletedEvent{
			PaymentID:   p.ID,
			TripID:      tripID,
			RiderID:     riderID,
			DriverID:    driverID,
			Amount:      amount,
			CompletedAt: now.Format(time.RFC3339),
		}
		if err := s.kafka.Publish(ctx, kafka.TopicPaymentCompleted, tripID, ev); err != nil {
			log.Printf("[payments] failed to publish payment.completed: %v", err)
		}
	}()

	return &p, nil
}

// GetByTripID fetches a payment by trip ID.
func (s *Service) GetByTripID(ctx context.Context, tripID string) (*Payment, error) {
	var p Payment
	err := s.db.QueryRow(ctx,
		`SELECT id, trip_id, rider_id, driver_id, amount, status, payment_method, created_at, completed_at
		 FROM payments WHERE trip_id=$1`, tripID).
		Scan(&p.ID, &p.TripID, &p.RiderID, &p.DriverID, &p.Amount, &p.Status, &p.PaymentMethod, &p.CreatedAt, &p.CompletedAt)
	if err != nil {
		return nil, errors.New("payment not found")
	}
	return &p, nil
}

// ListByUser returns paginated payments where user is rider or driver.
func (s *Service) ListByUser(ctx context.Context, userID string, limit, offset int) (*PaymentHistoryResponse, error) {
	var total int
	if err := s.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM payments WHERE rider_id=$1 OR driver_id=$1", userID).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, trip_id, rider_id, driver_id, amount, status, payment_method, created_at, completed_at
		 FROM payments WHERE rider_id=$1 OR driver_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	payments := []*Payment{}
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ID, &p.TripID, &p.RiderID, &p.DriverID, &p.Amount, &p.Status, &p.PaymentMethod, &p.CreatedAt, &p.CompletedAt); err != nil {
			return nil, err
		}
		payments = append(payments, &p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &PaymentHistoryResponse{Payments: payments, Total: total, Limit: limit, Offset: offset}, nil
}

// StartTripCompletedConsumer listens for trip.completed events and creates payments.
func (s *Service) StartTripCompletedConsumer(ctx context.Context) {
	s.kafka.Subscribe(ctx, kafka.TopicTripCompleted, "payment-trip-completed", func(data []byte) error {
		var ev events.TripCompletedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[payments] trip.completed: trip=%s fare=%.2f", ev.TripID, ev.Fare)
		_, err := s.CreatePayment(ctx, ev.TripID, ev.RiderID, ev.DriverID, ev.Fare)
		return err
	})
}
