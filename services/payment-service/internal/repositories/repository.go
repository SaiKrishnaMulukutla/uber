package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"uber/payment-service/internal/model"
)

// ErrAlreadyExists is returned by Create when a payment for the given trip_id
// already exists (ON CONFLICT DO NOTHING suppressed the INSERT).
var ErrAlreadyExists = errors.New("payment already exists for this trip")

// PaymentRepository defines all persistence operations for payments.
type PaymentRepository interface {
	Create(ctx context.Context, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod, providerName string, amount float64) (*model.Payment, error)
	MarkProcessing(ctx context.Context, id, providerOrderID string) error
	MarkAwaitingCashConfirm(ctx context.Context, id string) error
	MarkCompleted(ctx context.Context, id, providerPaymentID, providerSignature string, completedAt time.Time) error
	MarkFailed(ctx context.Context, id, reason string) error
	FindByID(ctx context.Context, id string) (*model.Payment, error)
	FindByTripID(ctx context.Context, tripID string) (*model.Payment, error)
	FindByProviderOrderID(ctx context.Context, providerOrderID string) (*model.Payment, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]*model.Payment, int, error)
	GetDriverEarnings(ctx context.Context, driverID string, from, to time.Time) ([]model.DailyEarning, float64, int, error)
}

type pgPaymentRepository struct {
	db *pgxpool.Pool
}

// NewRepository returns a PostgreSQL-backed PaymentRepository.
func NewRepository(db *pgxpool.Pool) PaymentRepository {
	return &pgPaymentRepository{db: db}
}

const selectCols = `id, trip_id, rider_id, rider_email, rider_phone, driver_id, amount, status, payment_method, provider,
	COALESCE(provider_order_id, ''), COALESCE(provider_payment_id, ''), COALESCE(failure_reason, ''),
	attempts_count, created_at, completed_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanPayment(row scanner) (*model.Payment, error) {
	p := &model.Payment{}
	err := row.Scan(
		&p.ID, &p.TripID, &p.RiderID, &p.RiderEmail, &p.RiderPhone, &p.DriverID, &p.Amount, &p.Status,
		&p.PaymentMethod, &p.Provider,
		&p.ProviderOrderID, &p.ProviderPaymentID, &p.FailureReason,
		&p.AttemptsCount, &p.CreatedAt, &p.CompletedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Create inserts a new PENDING payment. Returns ErrAlreadyExists when a
// payment for this trip_id already exists (ON CONFLICT DO NOTHING fired).
func (r *pgPaymentRepository) Create(ctx context.Context, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod, providerName string, amount float64) (*model.Payment, error) {
	const q = `INSERT INTO payments (trip_id, rider_id, rider_email, rider_phone, driver_id, amount, status, payment_method, provider)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', $7, $8)
		ON CONFLICT (trip_id) DO NOTHING
		RETURNING ` + selectCols
	p, err := scanPayment(r.db.QueryRow(ctx, q, tripID, riderID, riderEmail, riderPhone, driverID, amount, paymentMethod, providerName))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return p, nil
}

// MarkAwaitingCashConfirm transitions a cash payment to AWAITING_CASH_CONFIRM.
func (r *pgPaymentRepository) MarkAwaitingCashConfirm(ctx context.Context, id string) error {
	const q = `UPDATE payments SET status = 'AWAITING_CASH_CONFIRM', updated_at = NOW() WHERE id = $1 AND status = 'PENDING'`
	_, err := r.db.Exec(ctx, q, id)
	return err
}

// MarkProcessing transitions a payment to PROCESSING and stores the provider order ID.
func (r *pgPaymentRepository) MarkProcessing(ctx context.Context, id, providerOrderID string) error {
	const q = `UPDATE payments
		SET status = 'PROCESSING', provider_order_id = $2,
		    attempts_count = attempts_count + 1, updated_at = NOW()
		WHERE id = $1`
	_, err := r.db.Exec(ctx, q, id, providerOrderID)
	return err
}

// MarkCompleted transitions a payment to COMPLETED.
func (r *pgPaymentRepository) MarkCompleted(ctx context.Context, id, providerPaymentID, providerSignature string, completedAt time.Time) error {
	const q = `UPDATE payments
		SET status = 'COMPLETED', provider_payment_id = $2, provider_signature = $3,
		    completed_at = $4, updated_at = NOW()
		WHERE id = $1 AND status IN ('PENDING', 'PROCESSING', 'AWAITING_CASH_CONFIRM')`
	_, err := r.db.Exec(ctx, q, id, providerPaymentID, providerSignature, completedAt)
	return err
}

// MarkFailed transitions a payment to FAILED and records the reason.
func (r *pgPaymentRepository) MarkFailed(ctx context.Context, id, reason string) error {
	const q = `UPDATE payments
		SET status = 'FAILED', failure_reason = $2, updated_at = NOW()
		WHERE id = $1 AND status NOT IN ('COMPLETED', 'FAILED')`
	_, err := r.db.Exec(ctx, q, id, reason)
	return err
}

// FindByID retrieves a payment by its internal UUID.
func (r *pgPaymentRepository) FindByID(ctx context.Context, id string) (*model.Payment, error) {
	const q = `SELECT ` + selectCols + ` FROM payments WHERE id = $1`
	return scanPayment(r.db.QueryRow(ctx, q, id))
}

// FindByTripID retrieves a payment by its trip_id.
func (r *pgPaymentRepository) FindByTripID(ctx context.Context, tripID string) (*model.Payment, error) {
	const q = `SELECT ` + selectCols + ` FROM payments WHERE trip_id = $1`
	return scanPayment(r.db.QueryRow(ctx, q, tripID))
}

// FindByProviderOrderID retrieves a payment by its provider order ID (for webhook lookup).
func (r *pgPaymentRepository) FindByProviderOrderID(ctx context.Context, providerOrderID string) (*model.Payment, error) {
	const q = `SELECT ` + selectCols + ` FROM payments WHERE provider_order_id = $1`
	return scanPayment(r.db.QueryRow(ctx, q, providerOrderID))
}

// GetDriverEarnings returns per-day earnings for a driver within [from, to].
// Returns daily breakdown, total amount, and total trip count.
func (r *pgPaymentRepository) GetDriverEarnings(ctx context.Context, driverID string, from, to time.Time) ([]model.DailyEarning, float64, int, error) {
	const q = `
		SELECT
			TO_CHAR(completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
			SUM(amount)::float,
			COUNT(*)::int
		FROM payments
		WHERE driver_id = $1
		  AND status    = 'COMPLETED'
		  AND completed_at >= $2
		  AND completed_at <  $3
		GROUP BY day
		ORDER BY day DESC`

	rows, err := r.db.Query(ctx, q, driverID, from, to)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var (
		daily      []model.DailyEarning
		totalAmt   float64
		totalTrips int
	)
	for rows.Next() {
		var d model.DailyEarning
		if err := rows.Scan(&d.Date, &d.Amount, &d.Trips); err != nil {
			return nil, 0, 0, err
		}
		daily = append(daily, d)
		totalAmt += d.Amount
		totalTrips += d.Trips
	}
	if daily == nil {
		daily = []model.DailyEarning{}
	}
	return daily, totalAmt, totalTrips, rows.Err()
}

// ListByUser returns paginated payments for a rider or driver.
func (r *pgPaymentRepository) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*model.Payment, int, error) {
	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM payments WHERE rider_id = $1 OR driver_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}

	const q = `SELECT ` + selectCols + `
		FROM payments WHERE rider_id = $1 OR driver_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.db.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var payments []*model.Payment
	for rows.Next() {
		p, err := scanPayment(rows)
		if err != nil {
			return nil, 0, err
		}
		payments = append(payments, p)
	}
	return payments, total, rows.Err()
}
