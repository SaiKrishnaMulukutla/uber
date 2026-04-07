package tracking

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"uber/shared/pkg/jwt"
)

// safeConn wraps a websocket.Conn with a write mutex.
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
	mu       sync.RWMutex
	conns    map[string][]*safeConn
	upgrader websocket.Upgrader
}

// NewHub creates a tracking hub. allowedOrigins restricts which browser origins
// may open a WebSocket connection. Pass an empty slice to allow all origins
// (development only — never in production).
func NewHub(allowedOrigins []string) *Hub {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}
	checkOrigin := func(r *http.Request) bool {
		if len(allowed) == 0 {
			return true // dev mode
		}
		origin := r.Header.Get("Origin")
		_, ok := allowed[origin]
		return ok
	}
	return &Hub{
		conns: make(map[string][]*safeConn),
		upgrader: websocket.Upgrader{
			CheckOrigin: checkOrigin,
		},
	}
}

// Routes returns a chi.Router for the /ws mount point.
func (h *Hub) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/trips/{id}", h.HandleWS)
	return r
}

// HandleWS upgrades the connection and subscribes it to a trip.
// Requires a valid JWT access token passed as ?token=<jwt> (browsers cannot
// send custom headers during a WebSocket upgrade).
func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("token")
	if raw == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	claims, err := jwt.Validate(raw)
	if err != nil || claims.TokenType != "access" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	tripID := chi.URLParam(r, "id")
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}

	conn := &safeConn{ws: ws}

	h.mu.Lock()
	h.conns[tripID] = append(h.conns[tripID], conn)
	h.mu.Unlock()

	log.Printf("[ws] client connected to trip %s", tripID)

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
func (h *Hub) BroadcastLocation(tripID string, lat, lng float64) {
	h.mu.RLock()
	original := h.conns[tripID]
	snapshot := make([]*safeConn, len(original))
	copy(snapshot, original)
	h.mu.RUnlock()

	msg := map[string]any{
		"trip_id": tripID,
		"lat":     lat,
		"lng":     lng,
		"ts":      time.Now().Unix(),
	}

	for _, c := range snapshot {
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
