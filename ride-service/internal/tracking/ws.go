package tracking

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// safeConn wraps a websocket.Conn with a write mutex.
// gorilla/websocket allows one concurrent writer; this enforces that.
type safeConn struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (c *safeConn) writeJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.WriteJSON(v)
}

func (c *safeConn) readMessage() (int, []byte, error) {
	return c.ws.ReadMessage()
}

func (c *safeConn) close() { c.ws.Close() }

// Hub manages WebSocket connections per trip.
type Hub struct {
	mu    sync.RWMutex
	conns map[string][]*safeConn
}

// NewHub creates a tracking hub.
func NewHub() *Hub {
	return &Hub{conns: make(map[string][]*safeConn)}
}

// Routes returns a chi.Router for the /ws mount point.
func (h *Hub) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/trips/{id}", h.HandleWS)
	return r
}

// HandleWS upgrades the connection and subscribes it to a trip.
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "id")
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	conn := &safeConn{ws: ws}

	h.mu.Lock()
	h.conns[tripID] = append(h.conns[tripID], conn)
	h.mu.Unlock()

	log.Printf("[ws] client connected to trip %s", tripID)

	// Block until the client disconnects
	for {
		if _, _, err := conn.readMessage(); err != nil {
			break
		}
	}

	h.removeConn(tripID, conn)
	conn.close()
	log.Printf("[ws] client disconnected from trip %s", tripID)
}

// BroadcastLocation pushes a driver location update to all subscribers of a trip.
// Safe for concurrent calls — each safeConn serialises its own writes.
func (h *Hub) BroadcastLocation(tripID string, lat, lng float64) {
	h.mu.RLock()
	conns := h.conns[tripID]
	h.mu.RUnlock()

	msg := map[string]any{
		"trip_id": tripID,
		"lat":     lat,
		"lng":     lng,
		"ts":      time.Now().Unix(),
	}

	for _, c := range conns {
		if err := c.writeJSON(msg); err != nil {
			log.Printf("[ws] write error: %v", err)
		}
	}
}

func (h *Hub) removeConn(tripID string, conn *safeConn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	conns := h.conns[tripID]
	for i, c := range conns {
		if c == conn {
			h.conns[tripID] = append(conns[:i], conns[i+1:]...)
			break
		}
	}
	if len(h.conns[tripID]) == 0 {
		delete(h.conns, tripID)
	}
}
