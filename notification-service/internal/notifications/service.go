package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"uber/shared/events"
	"uber/shared/pkg/kafka"
)

// Service contains notification business logic.
type Service struct {
	db *pgxpool.Pool
}

// NewService creates a notification service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// Create inserts a new notification.
func (s *Service) Create(ctx context.Context, userID, notifType, title, body string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO notifications (user_id, type, title, body) VALUES ($1,$2,$3,$4)`,
		userID, notifType, title, body)
	return err
}

// ListByUser returns paginated notifications for a user.
func (s *Service) ListByUser(ctx context.Context, userID string, limit, offset int) (*NotificationListResponse, error) {
	var total int
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id=$1", userID).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, user_id, type, title, body, read, created_at
		 FROM notifications WHERE user_id=$1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notifs := []*Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		notifs = append(notifs, &n)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return &NotificationListResponse{Notifications: notifs, Total: total, Limit: limit, Offset: offset}, nil
}

// MarkRead marks a notification as read, verifying ownership.
func (s *Service) MarkRead(ctx context.Context, id, userID string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications SET read=TRUE WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("notification not found")
	}
	return nil
}

// StartEventConsumers subscribes to Kafka topics and generates notifications.
func (s *Service) StartEventConsumers(ctx context.Context, k *kafka.Client) {
	k.Subscribe(ctx, kafka.TopicRideRequested, "notif-ride-requested", func(data []byte) error {
		var ev events.RideRequestedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] ride.requested: trip=%s rider=%s", ev.TripID, ev.RiderID)
		return s.Create(ctx, ev.RiderID, "ride_requested", "Ride Requested", "Your ride is being matched with a driver.")
	})

	k.Subscribe(ctx, kafka.TopicDriverAssigned, "notif-driver-assigned", func(data []byte) error {
		var ev events.DriverAssignedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] driver.assigned: trip=%s driver=%s", ev.TripID, ev.DriverID)
		// Notify the driver about the new trip assignment
		return s.Create(ctx, ev.DriverID, "driver_assigned", "New Trip Assigned", fmt.Sprintf("You have been assigned to trip %s.", ev.TripID))
	})

	k.Subscribe(ctx, kafka.TopicTripCompleted, "notif-trip-completed", func(data []byte) error {
		var ev events.TripCompletedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] trip.completed: trip=%s fare=%.2f", ev.TripID, ev.Fare)
		_ = s.Create(ctx, ev.RiderID, "trip_completed", "Trip Completed", fmt.Sprintf("Your trip is complete. Fare: ₹%.2f", ev.Fare))
		if ev.DriverID != "" {
			_ = s.Create(ctx, ev.DriverID, "trip_completed", "Trip Completed", fmt.Sprintf("Trip completed. Earnings: ₹%.2f", ev.Fare))
		}
		return nil
	})

	k.Subscribe(ctx, kafka.TopicTripCancelled, "notif-trip-cancelled", func(data []byte) error {
		var ev events.TripCancelledEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] trip.cancelled: trip=%s", ev.TripID)
		_ = s.Create(ctx, ev.RiderID, "trip_cancelled", "Trip Cancelled", "Your trip has been cancelled.")
		if ev.DriverID != "" {
			_ = s.Create(ctx, ev.DriverID, "trip_cancelled", "Trip Cancelled", "The trip has been cancelled.")
		}
		return nil
	})

	k.Subscribe(ctx, kafka.TopicRatingSubmitted, "notif-rating-submitted", func(data []byte) error {
		var ev events.RatingSubmittedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] rating.submitted: ratee=%s score=%d", ev.RateeID, ev.Score)
		return s.Create(ctx, ev.RateeID, "rating_received", "New Rating",
			fmt.Sprintf("You received a %d-star rating.", ev.Score))
	})

	k.Subscribe(ctx, kafka.TopicPaymentCompleted, "notif-payment-completed", func(data []byte) error {
		var ev events.PaymentCompletedEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		log.Printf("[notifications] payment.completed: trip=%s amount=%.2f", ev.TripID, ev.Amount)
		return s.Create(ctx, ev.RiderID, "payment_completed", "Payment Processed",
			fmt.Sprintf("Payment of ₹%.2f has been processed for your trip.", ev.Amount))
	})
}
