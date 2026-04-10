package repositories

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"uber/payment-service/internal/model"
)

// PaymentRepository defines all persistence operations for payments.
type PaymentRepository interface {
	Create(ctx context.Context, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod, providerName string, amount float64) (*model.Payment, error)
	MarkProcessing(ctx context.Context, id, providerOrderID, providerQRID, providerQRURL string) error
	MarkAwaitingCashConfirm(ctx context.Context, id string) error
	MarkCompleted(ctx context.Context, id, providerPaymentID, providerSignature string, completedAt time.Time) error
	MarkFailed(ctx context.Context, id, reason string) error
	FindByID(ctx context.Context, id string) (*model.Payment, error)
	FindByTripID(ctx context.Context, tripID string) (*model.Payment, error)
	FindByProviderOrderID(ctx context.Context, providerOrderID string) (*model.Payment, error)
	FindByProviderQRID(ctx context.Context, qrID string) (*model.Payment, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]*model.Payment, int, error)
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
	COALESCE(provider_qr_id, ''), COALESCE(provider_qr_url, ''),
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
		&p.ProviderQRID, &p.ProviderQRURL,
		&p.AttemptsCount, &p.CreatedAt, &p.CompletedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Create inserts a new PENDING payment. Returns nil, nil on duplicate trip_id (idempotent).
func (r *pgPaymentRepository) Create(ctx context.Context, tripID, riderID, riderEmail, riderPhone, driverID, paymentMethod, providerName string, amount float64) (*model.Payment, error) {
	const q = `INSERT INTO payments (trip_id, rider_id, rider_email, rider_phone, driver_id, amount, status, payment_method, provider)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', $7, $8)
		ON CONFLICT (trip_id) DO NOTHING
		RETURNING ` + selectCols
	p, err := scanPayment(r.db.QueryRow(ctx, q, tripID, riderID, riderEmail, riderPhone, driverID, amount, paymentMethod, providerName))
	if err != nil {
		// pgx returns pgx.ErrNoRows when ON CONFLICT suppresses the INSERT
		return nil, nil //nolint:nilerr
	}
	return p, nil
}

// MarkAwaitingCashConfirm transitions a cash payment to AWAITING_CASH_CONFIRM.
func (r *pgPaymentRepository) MarkAwaitingCashConfirm(ctx context.Context, id string) error {
	const q = `UPDATE payments SET status = 'AWAITING_CASH_CONFIRM', updated_at = NOW() WHERE id = $1 AND status = 'PENDING'`
	_, err := r.db.Exec(ctx, q, id)
	return err
}

// MarkProcessing transitions a payment to PROCESSING and stores the provider order/QR IDs.
// Pass empty strings for providerQRID/providerQRURL when not applicable (card payments).
func (r *pgPaymentRepository) MarkProcessing(ctx context.Context, id, providerOrderID, providerQRID, providerQRURL string) error {
	const q = `UPDATE payments
		SET status = 'PROCESSING', provider_order_id = $2,
		    provider_qr_id = NULLIF($3, ''), provider_qr_url = NULLIF($4, ''),
		    attempts_count = attempts_count + 1, updated_at = NOW()
		WHERE id = $1`
	_, err := r.db.Exec(ctx, q, id, providerOrderID, providerQRID, providerQRURL)
	return err
}

// MarkCompleted transitions a payment to COMPLETED.
// The status guard prevents a concurrent webhook + verify from double-completing.
func (r *pgPaymentRepository) MarkCompleted(ctx context.Context, id, providerPaymentID, providerSignature string, completedAt time.Time) error {
	const q = `UPDATE payments
		SET status = 'COMPLETED', provider_payment_id = $2, provider_signature = $3,
		    completed_at = $4, updated_at = NOW()
		WHERE id = $1 AND status IN ('PENDING', 'PROCESSING', 'AWAITING_CASH_CONFIRM')`
	_, err := r.db.Exec(ctx, q, id, providerPaymentID, providerSignature, completedAt)
	return err
}

// MarkFailed transitions a payment to FAILED and records the reason.
// The status guard prevents overwriting a COMPLETED or already-FAILED payment.
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

// FindByProviderQRID retrieves a payment by its Razorpay QR code ID (for qr_code.credited webhook).
func (r *pgPaymentRepository) FindByProviderQRID(ctx context.Context, qrID string) (*model.Payment, error) {
	const q = `SELECT ` + selectCols + ` FROM payments WHERE provider_qr_id = $1`
	return scanPayment(r.db.QueryRow(ctx, q, qrID))
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
