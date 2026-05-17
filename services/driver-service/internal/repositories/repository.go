package repositories

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"uber/driver-service/internal/model"
)

// DriverRepository defines persistence operations for drivers.
type DriverRepository interface {
	EmailExists(ctx context.Context, email string) (bool, error)
	PhoneExists(ctx context.Context, phone string) (bool, error)
	Create(ctx context.Context, d *model.Driver, passwordHash string) error
	FindByEmail(ctx context.Context, email string) (*model.Driver, string, error)
	FindByID(ctx context.Context, id string) (*model.Driver, error)
	UpdateStatus(ctx context.Context, driverID, status string) error
	UpdateRating(ctx context.Context, driverID string, score int) error
	Update(ctx context.Context, id, name, phone, vehicleType, licensePlate string) (*model.Driver, error)
	UpdatePassword(ctx context.Context, id, hash string) error
	GetAvailableDriverIDs(ctx context.Context) ([]string, error)
}

type pgDriverRepository struct{ pool *pgxpool.Pool }

// NewRepository returns a Postgres-backed DriverRepository.
func NewRepository(pool *pgxpool.Pool) DriverRepository {
	return &pgDriverRepository{pool: pool}
}

func (r *pgDriverRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM drivers WHERE email=$1)", email).Scan(&exists)
	return exists, err
}

func (r *pgDriverRepository) PhoneExists(ctx context.Context, phone string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM drivers WHERE phone=$1)", phone).Scan(&exists)
	return exists, err
}

func (r *pgDriverRepository) Create(ctx context.Context, d *model.Driver, passwordHash string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO drivers (id,name,email,phone,password_hash,vehicle_type,license_plate,status,rating,rating_count)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		d.ID, d.Name, d.Email, d.Phone, passwordHash,
		d.VehicleType, d.LicensePlate, d.Status, d.Rating, d.RatingCount)
	return err
}

// FindByEmail returns the driver and the password hash for login.
func (r *pgDriverRepository) FindByEmail(ctx context.Context, email string) (*model.Driver, string, error) {
	var d model.Driver
	var hash string
	err := r.pool.QueryRow(ctx,
		`SELECT id,name,email,phone,password_hash,vehicle_type,license_plate,status,rating,rating_count,created_at
		 FROM drivers WHERE email=$1`, email).
		Scan(&d.ID, &d.Name, &d.Email, &d.Phone, &hash,
			&d.VehicleType, &d.LicensePlate, &d.Status, &d.Rating, &d.RatingCount, &d.CreatedAt)
	if err != nil {
		return nil, "", errors.New("invalid credentials")
	}
	return &d, hash, nil
}

func (r *pgDriverRepository) FindByID(ctx context.Context, id string) (*model.Driver, error) {
	var d model.Driver
	err := r.pool.QueryRow(ctx,
		`SELECT id,name,email,phone,vehicle_type,license_plate,status,rating,rating_count,created_at
		 FROM drivers WHERE id=$1`, id).
		Scan(&d.ID, &d.Name, &d.Email, &d.Phone,
			&d.VehicleType, &d.LicensePlate, &d.Status, &d.Rating, &d.RatingCount, &d.CreatedAt)
	if err != nil {
		return nil, errors.New("driver not found")
	}
	return &d, nil
}

func (r *pgDriverRepository) Update(ctx context.Context, id, name, phone, vehicleType, licensePlate string) (*model.Driver, error) {
	var d model.Driver
	err := r.pool.QueryRow(ctx,
		`UPDATE drivers SET
		   name          = COALESCE(NULLIF($1, ''), name),
		   phone         = COALESCE(NULLIF($2, ''), phone),
		   vehicle_type  = COALESCE(NULLIF($3, ''), vehicle_type),
		   license_plate = COALESCE(NULLIF($4, ''), license_plate)
		 WHERE id = $5
		 RETURNING id, name, email, phone, vehicle_type, license_plate, status, rating, rating_count, created_at`,
		name, phone, vehicleType, licensePlate, id).
		Scan(&d.ID, &d.Name, &d.Email, &d.Phone,
			&d.VehicleType, &d.LicensePlate, &d.Status, &d.Rating, &d.RatingCount, &d.CreatedAt)
	if err != nil {
		return nil, errors.New("driver not found")
	}
	return &d, nil
}

// UpdateStatus sets status in Postgres only — Redis GEO sync is handled by service.
func (r *pgDriverRepository) UpdateStatus(ctx context.Context, driverID, status string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE drivers SET status=$1 WHERE id=$2`, status, driverID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("driver not found")
	}
	return nil
}

func (r *pgDriverRepository) UpdatePassword(ctx context.Context, id, hash string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE drivers SET password_hash=$1 WHERE id=$2`, hash, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("driver not found")
	}
	return nil
}

// UpdateRating applies an incremental rolling average update.
func (r *pgDriverRepository) UpdateRating(ctx context.Context, driverID string, score int) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE drivers SET
		   rating_count = rating_count + 1,
		   rating = rating + ($1::float - rating) / (rating_count + 1)
		 WHERE id = $2`, score, driverID)
	return err
}

// GetAvailableDriverIDs returns the IDs of all drivers currently marked available in Postgres.
// Used during startup reconciliation to re-populate the Redis GEO set.
func (r *pgDriverRepository) GetAvailableDriverIDs(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT id FROM drivers WHERE status = 'available'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
