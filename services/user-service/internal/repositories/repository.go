package repositories

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"uber/user-service/internal/model"
)

type UserRepository interface {
	EmailExists(ctx context.Context, email string) (bool, error)
	PhoneExists(ctx context.Context, phone string) (bool, error)
	Create(ctx context.Context, id, name, email, phone, hash string) error
	FindByEmail(ctx context.Context, email string) (*model.User, string, error)
	FindByID(ctx context.Context, id string) (*model.User, error)
	UpdateRating(ctx context.Context, userID string, score int) error
}

type pgUserRepository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) UserRepository {
	return &pgUserRepository{db: db}
}

func (r *pgUserRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)", email).Scan(&exists)
	return exists, err
}

func (r *pgUserRepository) PhoneExists(ctx context.Context, phone string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE phone=$1)", phone).Scan(&exists)
	return exists, err
}

func (r *pgUserRepository) Create(ctx context.Context, id, name, email, phone, hash string) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO users (id,name,email,phone,password_hash,rating,rating_count) VALUES ($1,$2,$3,$4,$5,5.0,0)`,
		id, name, email, phone, hash)
	return err
}

func (r *pgUserRepository) FindByEmail(ctx context.Context, email string) (*model.User, string, error) {
	var u model.User
	var hash string
	err := r.db.QueryRow(ctx,
		`SELECT id,name,email,phone,password_hash,rating,rating_count,created_at FROM users WHERE email=$1`,
		email).Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &hash, &u.Rating, &u.RatingCount, &u.CreatedAt)
	if err != nil {
		return nil, "", err
	}
	return &u, hash, nil
}

func (r *pgUserRepository) FindByID(ctx context.Context, id string) (*model.User, error) {
	var u model.User
	err := r.db.QueryRow(ctx,
		`SELECT id,name,email,phone,rating,rating_count,created_at FROM users WHERE id=$1`, id).
		Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &u.Rating, &u.RatingCount, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *pgUserRepository) UpdateRating(ctx context.Context, userID string, score int) error {
	_, err := r.db.Exec(ctx,
		`UPDATE users SET
		   rating_count = rating_count + 1,
		   rating = rating + ($1::float - rating) / (rating_count + 1)
		 WHERE id = $2`, score, userID)
	return err
}
