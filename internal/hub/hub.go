// Package hub fans out note-change events to every connected browser over
// WebSocket, so a save by one housemate shows up live for everyone else
// without them refreshing (broadcast-on-save — no CRDT/merge logic).
package hub

import "sync"

// sendBuffer bounds how many queued messages a slow client tolerates
// before we drop it rather than let one stuck browser back up the rest.
const sendBuffer = 16

type Client struct {
	send chan []byte
}

func (c *Client) Send() <-chan []byte {
	return c.send
}

type Hub struct {
	mu      sync.Mutex
	clients map[*Client]struct{}
}

func New() *Hub {
	return &Hub{clients: make(map[*Client]struct{})}
}

func (h *Hub) Register() *Client {
	c := &Client{send: make(chan []byte, sendBuffer)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
}

// Broadcast fans msg out to every connected client. A client whose send
// buffer is already full is skipped for this message rather than blocking
// everyone else.
func (h *Hub) Broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
		}
	}
}
