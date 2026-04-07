package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	
	"uber/trip-service/internal/model"
)

// TripRepository defines persistence operations for trips.
type TripRepository interface {
	CreateTrip(ctx context.Context, t *model.Trip) error
	FindByID(ctx context.Context, id string) (*model.Trip, error)
	AssignDriver(ctx context.Context, tripID, driverID string) error
	StartTrip(ctx context.Context, tripID string, now time.Time) error
	EndTrip(ctx context.Context, tripID string, fare float64, durationSeconds int64, now time.Time) error
	CancelTrip(ctx context.Context, tripID string, now time.Time) error
	ListByRider(ctx context.Context, riderID string, limit, offset int) ([]*model.Trip, int, error)
	ListByDriver(ctx context.Context, driverID string, limit, offset int) ([]*model.Trip, int, error)
	CreateRating(ctx context.Context, tripID, raterID, raterRole, rateeID, rateeRole string, score int, comment string) (*model.Rating, error)
}

type pgTripRepository struct{ pool *pgxpool.Pool }

// NewRepository returns a Postgres-backed TripRepository.
func NewRepository(pool *pgxpool.Pool) TripRepository {
	return &pgTripRepository{pool: pool}
}

func (r *pgTripRepository) CreateTrip(ctx context.Context, t *model.Trip) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO trips (id,rider_id,rider_email,pickup_lat,pickup_lng,drop_lat,drop_lng,status,payment_method,requested_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		t.ID, t.RiderID, t.RiderEmail, t.PickupLat, t.PickupLng, t.DropLat, t.DropLng, t.Status, t.PaymentMethod, t.RequestedAt)
	return err
}

func (r *pgTripRepository) FindByID(ctx context.Context, id string) (*model.Trip, error) {
	var t model.Trip
	err := r.pool.QueryRow(ctx,
		`SELECT id,rider_id,rider_email,driver_id,pickup_lat,pickup_lng,drop_lat,drop_lng,
		        fare,status,payment_method,requested_at,started_at,completed_at,duration_seconds,created_at
		 FROM trips WHERE id=$1`, id).
		Scan(&t.ID, &t.RiderID, &t.RiderEmail, &t.DriverID,
			&t.PickupLat, &t.PickupLng, &t.DropLat, &t.DropLng,
			&t.Fare, &t.Status, &t.PaymentMethod, &t.RequestedAt, &t.StartedAt, &t.CompletedAt, &t.DurationSeconds, &t.CreatedAt)
	if err != nil {
		return nil, errors.New("trip not found")
	}
	return &t, nil
}

func (r *pgTripRepository) AssignDriver(ctx context.Context, tripID, driverID string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE trips SET driver_id=$1, status=$2
		 WHERE id=$3 AND status=$4`,
		driverID, model.StatusDriverAssigned, tripID, model.StatusRequested)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("trip not found or invalid state for assignment")
	}
	return nil
}

func (r *pgTripRepository) StartTrip(ctx context.Context, tripID string, now time.Time) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE trips SET status=$1, started_at=$2 WHERE id=$3 AND status=$4`,
		model.StatusStarted, now, tripID, model.StatusDriverAssigned)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("trip not found or not in DRIVER_ASSIGNED state")
	}
	return nil
}

func (r *pgTripRepository) EndTrip(ctx context.Context, tripID string, fare float64, durationSeconds int64, now time.Time) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE trips SET status=$1, fare=$2, duration_seconds=$3, completed_at=$4 WHERE id=$5 AND status=$6`,
		model.StatusCompleted, fare, durationSeconds, now, tripID, model.StatusStarted)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("trip not in STARTED state")
	}
	return nil
}

func (r *pgTripRepository) CancelTrip(ctx context.Context, tripID string, now time.Time) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE trips SET status=$1, completed_at=$2 WHERE id=$3 AND status IN ($4,$5)`,
		model.StatusCancelled, now, tripID,
		model.StatusRequested, model.StatusDriverAssigned)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("trip already transitioned or not found")
	}
	return nil
}

func (r *pgTripRepository) ListByRider(ctx context.Context, riderID string, limit, offset int) ([]*model.Trip, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM trips WHERE rider_id=$1", riderID).Scan(&total); err != nil {
		return nil, 0, err
	}
	return r.queryTrips(ctx, total, `SELECT id,rider_id,rider_email,driver_id,pickup_lat,pickup_lng,drop_lat,drop_lng,
	        fare,status,payment_method,requested_at,started_at,completed_at,duration_seconds,created_at
	 FROM trips WHERE rider_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, riderID, limit, offset)
}

func (r *pgTripRepository) ListByDriver(ctx context.Context, driverID string, limit, offset int) ([]*model.Trip, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM trips WHERE driver_id=$1", driverID).Scan(&total); err != nil {
		return nil, 0, err
	}
	return r.queryTrips(ctx, total, `SELECT id,rider_id,rider_email,driver_id,pickup_lat,pickup_lng,drop_lat,drop_lng,
	        fare,status,payment_method,requested_at,started_at,completed_at,duration_seconds,created_at
	 FROM trips WHERE driver_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, driverID, limit, offset)
}

func (r *pgTripRepository) queryTrips(ctx context.Context, total int, query string, args ...any) ([]*model.Trip, int, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	trips := []*model.Trip{}
	for rows.Next() {
		var t model.Trip
		if err := rows.Scan(&t.ID, &t.RiderID, &t.RiderEmail, &t.DriverID,
			&t.PickupLat, &t.PickupLng, &t.DropLat, &t.DropLng,
			&t.Fare, &t.Status, &t.PaymentMethod, &t.RequestedAt, &t.StartedAt, &t.CompletedAt, &t.DurationSeconds, &t.CreatedAt); err != nil {
			return nil, 0, err
		}
		trips = append(trips, &t)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return trips, total, nil
}

func (r *pgTripRepository) CreateRating(ctx context.Context, tripID, raterID, raterRole, rateeID, rateeRole string, score int, comment string) (*model.Rating, error) {
	var rating model.Rating
	err := r.pool.QueryRow(ctx,
		`INSERT INTO ratings (trip_id, rater_id, rater_role, ratee_id, ratee_role, score, comment)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (trip_id, rater_id) DO NOTHING
		 RETURNING id, trip_id, rater_id, rater_role, ratee_id, ratee_role, score, comment, created_at`,
		tripID, raterID, raterRole, rateeID, rateeRole, score, comment).
		Scan(&rating.ID, &rating.TripID, &rating.RaterID, &rating.RaterRole,
			&rating.RateeID, &rating.RateeRole, &rating.Score, &rating.Comment, &rating.CreatedAt)
	if err != nil {
		// ON CONFLICT DO NOTHING returns no rows — fetch the existing rating.
		fetchErr := r.pool.QueryRow(ctx,
			`SELECT id, trip_id, rater_id, rater_role, ratee_id, ratee_role, score, comment, created_at
			 FROM ratings WHERE trip_id=$1 AND rater_id=$2`,
			tripID, raterID).
			Scan(&rating.ID, &rating.TripID, &rating.RaterID, &rating.RaterRole,
				&rating.RateeID, &rating.RateeRole, &rating.Score, &rating.Comment, &rating.CreatedAt)
		if fetchErr != nil {
			return nil, errors.New("rating creation failed")
		}
	}
	return &rating, nil
}
