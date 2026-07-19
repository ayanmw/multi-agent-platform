package ws

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Client struct {
	ID     string
	Hub    *Hub
	Send   chan event.Event
	Conn   *websocket.Conn
	mu     sync.Mutex
	closed bool
}

// ClientControlMsg represents a control message sent from the client
type ClientControlMsg struct {
	Action     string `json:"action"`      // pause, resume, cancel, approve, deny
	TaskID     string `json:"task_id"`
	AgentID    string `json:"agent_id"`
	ApprovalID string `json:"approval_id"` // Phase 5: 审批请求 ID
}

// ControlHandler is called when a client sends a control message
type ControlHandler func(msg ClientControlMsg)

const defaultEventBufferSize = 1000

// eventBuffer is a fixed-capacity circular buffer for recently broadcast events.
// It keeps a bounded in-memory history so clients that reconnect after a brief
// disconnect can request events they missed since their last known event_id.
type eventBuffer struct {
	//events holds events in insertion order; oldest at index 0.
	events []event.Event
	//index maps event_id to its position in events for O(1) lookup.
	index map[string]int
	//capacity is the maximum number of events retained.
	capacity int
	mu       sync.RWMutex
}

func newEventBuffer(capacity int) *eventBuffer {
	if capacity <= 0 {
		capacity = defaultEventBufferSize
	}
	return &eventBuffer{
		events:   make([]event.Event, 0, capacity),
		index:    make(map[string]int),
		capacity: capacity,
	}
}

// append adds an event to the buffer, evicting the oldest event when full.
func (b *eventBuffer) append(evt event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.events) == b.capacity {
		// 环形缓冲区满时移除最旧事件，避免 index 无限增长。
		oldest := b.events[0]
		delete(b.index, oldest.EventID)
		b.events = b.events[1:]
		// 剩余事件的下标整体前移一位。
		for id, idx := range b.index {
			b.index[id] = idx - 1
		}
	}
	b.index[evt.EventID] = len(b.events)
	b.events = append(b.events, evt)
}

// eventsAfter returns up to limit events strictly after sinceEventID,
// ordered by ascending timestamp / insertion order.
// If sinceEventID is not found in the buffer, it returns ErrEventIDNotFound
// so the caller can ask the client to fall back to a full replay.
func (b *eventBuffer) eventsAfter(sinceEventID string, limit int) ([]event.Event, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	idx, ok := b.index[sinceEventID]
	if !ok {
		return nil, ErrEventIDNotFound
	}
	// 只返回 sinceEventID 之后的事件（严格之后）。
	start := idx + 1
	if start >= len(b.events) {
		return []event.Event{}, nil
	}
	end := start + limit
	if end > len(b.events) {
		end = len(b.events)
	}
	out := make([]event.Event, end-start)
	copy(out, b.events[start:end])
	return out, nil
}

// ErrEventIDNotFound indicates the requested event_id is no longer in the
// short-term buffer (disconnected too long or server restarted).
var ErrEventIDNotFound = errors.New("event_id not found in replay buffer")

type Hub struct {
	clients        map[*Client]bool
	register       chan *Client
	unregister     chan *Client
	broadcast      chan event.Event
	controlHandler ControlHandler
	mu             sync.RWMutex
	//eventBuf caches recently broadcast events for reconnect replay.
	eventBuf *eventBuffer
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan event.Event),
		eventBuf:   newEventBuffer(defaultEventBufferSize),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Printf("Client connected: %s (total: %d)", client.ID, len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)
			}
			h.mu.Unlock()
			log.Printf("Client disconnected: %s (total: %d)", client.ID, len(h.clients))

		case evt := <-h.broadcast:
			// 先把事件写入环形缓冲区，再广播；这样重连 replay 能拿到完整缓存。
			h.eventBuf.append(evt)
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.Send <- evt:
				default:
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) SendEvent(evt event.Event) {
	h.broadcast <- evt
}

// ReplayEvents returns cached events strictly after sinceEventID.
// The result is ordered by ascending timestamp/insertion order and capped at
// limit. If sinceEventID is not in the buffer it returns ErrEventIDNotFound,
// signaling that the client has been disconnected too long and should fall
// back to a full task replay.
func (h *Hub) ReplayEvents(sinceEventID string, limit int) ([]event.Event, error) {
	return h.eventBuf.eventsAfter(sinceEventID, limit)
}

// SetControlHandler registers a handler for client control messages
func (h *Hub) SetControlHandler(handler ControlHandler) {
	h.controlHandler = handler
}

func ServeWS(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket upgrade error: %v", err)
			return
		}

		client := &Client{
			ID:    generateID(),
			Hub:   hub,
			Send: make(chan event.Event, 256),
			Conn: conn,
		}

		hub.register <- client
		go client.writePump()
		go client.readPump()
	}
}

func (c *Client) readPump() {
	defer func() {
		c.Hub.unregister <- c
		c.Conn.Close()
	}()

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}

		// Try to parse as control message
		var msg ClientControlMsg
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Client %s: unparseable message: %s", c.ID, string(message))
			continue
		}

		// Route to control handler if registered
		if c.Hub.controlHandler != nil {
			go c.Hub.controlHandler(msg)
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			if !ok {
				// Hub closed the channel
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()

			// Marshal event to JSON and send
			data, err := json.Marshal(message)
			if err != nil {
				log.Printf("writePump: marshal error: %v", err)
				continue
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("writePump: write error: %v", err)
				return
			}

		case <-ticker.C:
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func generateID() string {
	return "client_" + time.Now().Format("20060102150405")
}
