package dashboard

import (
	"context"
	"sync"

	"nhooyr.io/websocket"
)

// client wraps a single browser WebSocket connection.
type client struct {
	conn *websocket.Conn
}

// Hub manages connected browser clients and broadcasts snapshots to them.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
}

// NewHub creates a Hub.
func NewHub() *Hub {
	return &Hub{clients: make(map[*client]struct{})}
}

// register adds a client to the hub.
func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

// unregister removes a client from the hub.
func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Broadcast sends payload to all connected clients.
// Clients that fail to receive are removed.
func (h *Hub) Broadcast(ctx context.Context, payload []byte) {
	h.mu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()

	for _, c := range clients {
		if err := c.conn.Write(ctx, websocket.MessageText, payload); err != nil {
			h.unregister(c)
			c.conn.Close(websocket.StatusGoingAway, "write error")
		}
	}
}
