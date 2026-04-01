package repositories

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"uber/notification-service/internal/model"
)

type NotificationRepository interface {
	Create(ctx context.Context, userID, notifType, title, body string) error
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]*model.Notification, int, error)
	MarkRead(ctx context.Context, id, userID string) error
}

type pgNotificationRepository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) NotificationRepository {
	return &pgNotificationRepository{db: db}
}

func (r *pgNotificationRepository) Create(ctx context.Context, userID, notifType, title, body string) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO notifications (user_id, type, title, body) VALUES ($1,$2,$3,$4)`,
		userID, notifType, title, body)
	return err
}

func (r *pgNotificationRepository) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*model.Notification, int, error) {
	var total int
	if err := r.db.QueryRow(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id=$1", userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(ctx,
		`SELECT id, user_id, type, title, body, read, created_at
		 FROM notifications WHERE user_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	notifs := []*model.Notification{}
	for rows.Next() {
		var n model.Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &n.Read, &n.CreatedAt); err != nil {
			return nil, 0, err
		}
		notifs = append(notifs, &n)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return notifs, total, nil
}

func (r *pgNotificationRepository) MarkRead(ctx context.Context, id, userID string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE notifications SET read=TRUE WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("notification not found")
	}
	return nil
}
