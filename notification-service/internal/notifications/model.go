package notifications

import "time"

// Notification represents a user notification.
type Notification struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

// NotificationListResponse is the paginated response for GET /notifications.
type NotificationListResponse struct {
	Notifications []*Notification `json:"notifications"`
	Total         int             `json:"total"`
	Limit         int             `json:"limit"`
	Offset        int             `json:"offset"`
}
