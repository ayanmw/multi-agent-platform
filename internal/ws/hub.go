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

// ClientControlMsg 表示从客户端发来的 control message
type ClientControlMsg struct {
	Action     string `json:"action"`      // pause、resume、cancel、approve、deny
	TaskID     string `json:"task_id"`
	AgentID    string `json:"agent_id"`
	ApprovalID string `json:"approval_id"` // Phase 5: 审批请求 ID
}

// ControlHandler 在客户端发来 control message 时被调用
type ControlHandler func(msg ClientControlMsg)

const defaultEventBufferSize = 1000

// eventBuffer 是一个固定容量的环形缓冲区，用于缓存最近广播的事件。
// 它保留有界的内存历史，使短暂断连后重连的客户端可以请求自其上次已知
// event_id 之后错过的事件。
type eventBuffer struct {
	//events 按插入顺序保存事件；最旧的在 index 0。
	events []event.Event
	//index 将 event_id 映射到其在 events 中的位置，用于 O(1) 查找。
	index map[string]int
	//capacity 是保留事件的最大数量。
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

// append 将一个事件添加到缓冲区，缓冲区满时驱逐最旧的事件。
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

// eventsAfter 返回 sinceEventID 严格之后最多 limit 条事件，
// 按时间/插入顺序升序返回。
// 若 sinceEventID 不在缓冲区中，则返回 ErrEventIDNotFound，
// 以便调用方让客户端回退到完整 replay。
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

// ErrEventIDNotFound 表示请求的 event_id 已不在短期缓冲区中
// （断连时间过长或服务器重启）。
var ErrEventIDNotFound = errors.New("event_id not found in replay buffer")

type Hub struct {
	clients        map[*Client]bool
	register       chan *Client
	unregister     chan *Client
	broadcast      chan event.Event
	controlHandler ControlHandler
	mu             sync.RWMutex
	//eventBuf 缓存最近广播的事件，用于断连重连后的 replay。
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

// ReplayEvents 返回 sinceEventID 严格之后的缓存事件。
// 结果按时间/插入顺序升序排列，并以 limit 为上限。若 sinceEventID 不在缓冲区中，
// 则返回 ErrEventIDNotFound，表示客户端已断连过久，应回退到完整的 task replay。
func (h *Hub) ReplayEvents(sinceEventID string, limit int) ([]event.Event, error) {
	return h.eventBuf.eventsAfter(sinceEventID, limit)
}

// SetControlHandler 注册一个用于处理客户端 control message 的 handler
func (h *Hub) SetControlHandler(handler ControlHandler) {
	h.controlHandler = handler
}

// RegisterTestClient 注册一个 client 用于接收广播事件，返回该 client。
// 仅用于测试：生产路径的 client 由 ServeWS 经 websocket 升级创建。测试用它
// 直接从 client.Send chan 读取广播事件，无需真实 websocket 连接。
func (h *Hub) RegisterTestClient(id string) *Client {
	c := &Client{
		ID:   id,
		Hub:  h,
		Send: make(chan event.Event, 256),
	}
	h.register <- c
	return c
}

// UnregisterTestClient 注销一个测试 client。仅用于测试。
func (h *Hub) UnregisterTestClient(c *Client) {
	h.unregister <- c
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

		// 尝试解析为 control message
		var msg ClientControlMsg
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Client %s: unparseable message: %s", c.ID, string(message))
			continue
		}

		// 若已注册 control handler 则路由给它
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
				// Hub 已关闭该 channel
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()

			// 将 event 序列化为 JSON 并发送
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
