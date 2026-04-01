package repositories

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"uber/payment-service/internal/model"
)

type PaymentRepository interface {
	Create(ctx context.Context, tripID, riderID, driverID string, amount float64) (*model.Payment, error)
	MarkCompleted(ctx context.Context, id string, completedAt time.Time) error
	FindByTripID(ctx context.Context, tripID string) (*model.Payment, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]*model.Payment, int, error)
}

type pgPaymentRepository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) PaymentRepository {
	return &pgPaymentRepository{db: db}
}

func (r *pgPaymentRepository) Create(ctx context.Context, tripID, riderID, driverID string, amount float64) (*model.Payment, error) {
	var p model.Payment
	err := r.db.QueryRow(ctx,
		`INSERT INTO payments (trip_id, rider_id, driver_id, amount, status, payment_method)
		 VALUES ($1,$2,$3,$4,$5,'cash')
		 ON CONFLICT (trip_id) DO UPDATE SET trip_id=EXCLUDED.trip_id
		 RETURNING id, trip_id, rider_id, driver_id, amount, status, payment_method, created_at, completed_at`,
		tripID, riderID, driverID, amount, model.StatusPending).
		Scan(&p.ID, &p.TripID, &p.RiderID, &p.DriverID, &p.Amount, &p.Status, &p.PaymentMethod, &p.CreatedAt, &p.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *pgPaymentRepository) MarkCompleted(ctx context.Context, id string, completedAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE payments SET status=$1, completed_at=$2 WHERE id=$3`,
		model.StatusCompleted, completedAt, id)
	return err
}

func (r *pgPaymentRepository) FindByTripID(ctx context.Context, tripID string) (*model.Payment, error) {
	var p model.Payment
	err := r.db.QueryRow(ctx,
		`SELECT id, trip_id, rider_id, driver_id, amount, status, payment_method, created_at, completed_at
		 FROM payments WHERE trip_id=$1`, tripID).
		Scan(&p.ID, &p.TripID, &p.RiderID, &p.DriverID, &p.Amount, &p.Status, &p.PaymentMethod, &p.CreatedAt, &p.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *pgPaymentRepository) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*model.Payment, int, error) {
	var total int
	if err := r.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM payments WHERE rider_id=$1 OR driver_id=$1", userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(ctx,
		`SELECT id, trip_id, rider_id, driver_id, amount, status, payment_method, created_at, completed_at
		 FROM payments WHERE rider_id=$1 OR driver_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	payments := []*model.Payment{}
	for rows.Next() {
		var p model.Payment
		if err := rows.Scan(&p.ID, &p.TripID, &p.RiderID, &p.DriverID, &p.Amount, &p.Status, &p.PaymentMethod, &p.CreatedAt, &p.CompletedAt); err != nil {
			return nil, 0, err
		}
		payments = append(payments, &p)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return payments, total, nil
}
