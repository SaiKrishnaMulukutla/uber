package hub

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// PaymentHub manages WebSocket subscribers per payment ID.
// When a payment completes, Broadcast notifies all connected clients.
type PaymentHub struct {
	mu          sync.Mutex
	subscribers map[string][]chan struct{}
}

// New returns an initialised PaymentHub.
func New() *PaymentHub {
	return &PaymentHub{subscribers: make(map[string][]chan struct{})}
}

// Broadcast sends a completion signal to every client watching paymentID.
func (h *PaymentHub) Broadcast(paymentID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subscribers[paymentID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *PaymentHub) subscribe(paymentID string) chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subscribers[paymentID] = append(h.subscribers[paymentID], ch)
	h.mu.Unlock()
	return ch
}

func (h *PaymentHub) unsubscribe(paymentID string, ch chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.subscribers[paymentID]
	for i, c := range list {
		if c == ch {
			h.subscribers[paymentID] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(h.subscribers[paymentID]) == 0 {
		delete(h.subscribers, paymentID)
	}
}

// HandleWS upgrades the connection and waits for a payment completion signal.
// The client receives {"status":"completed"} and the connection closes.
func (h *PaymentHub) HandleWS(w http.ResponseWriter, r *http.Request, paymentID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[hub] ws upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	ch := h.subscribe(paymentID)
	defer h.unsubscribe(paymentID, ch)

	// Wait for completion signal or client disconnect
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case <-ch:
		conn.WriteJSON(map[string]string{"status": "completed"})
	case <-done:
		// client disconnected
	}
}
