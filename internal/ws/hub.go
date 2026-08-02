package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"

	"github.com/gorilla/websocket"
	"github.com/anmingwei/multi-agent-platform/internal/observability"
)

// log 是 observability.DefaultLogger 的包级别别名，便于结构化日志埋点调用。
var log = observability.DefaultLogger

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
	Action     string   `json:"action"`      // pause、resume、cancel、approve、deny、set_auto_approval_tags
	TaskID     string   `json:"task_id"`
	AgentID    string   `json:"agent_id"`
	ApprovalID string   `json:"approval_id"` // Phase 5: 审批请求 ID
	Tags       []string `json:"tags,omitempty"` // 自动审批标签列表（action=set_auto_approval_tags 时使用）
}

// ControlHandler 在客户端发来 control message 时被调用
type ControlHandler func(msg ClientControlMsg)

const defaultEventBufferSize = 5000

// criticalEventReserve 是缓冲区中保留给终态/同步事件的最低席位数。
// 当满时尝试优先驱逐普通事件，避免 task_failed/task_completed/task_status_sync
// 被大量 llm_delta 挤掉导致断线重连无法恢复真实任务状态。

// isTerminalEvent 判断事件类型是否为任务终态或状态修正事件。
// 这些事件在 eventBuffer 中应被优先保留，避免被大量 delta 挤掉。
func isTerminalEvent(evt event.Event) bool {
	switch evt.Type {
	case "task_failed",
		"task_completed",
		event.EventTaskStatusSync:
		return true
	default:
		return false
	}
}

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

// append 将一个事件添加到缓冲区，缓冲区满时驱逐最旧的非关键事件；
// 若全部被保留区覆盖的事件均为关键事件，则驱逐最旧的关键事件。
func (b *eventBuffer) append(evt event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.events) == b.capacity {
		b.evictOne(evt)
	}
	b.index[evt.EventID] = len(b.events)
	b.events = append(b.events, evt)
}

// evictOne 移除一条事件以腾出空间。新事件为关键事件时直接驱逐最旧事件；
// 否则优先驱逐保留区之外的非关键事件，尽力保留 terminal/status_sync 事件。
func (b *eventBuffer) evictOne(incoming event.Event) {
	if isTerminalEvent(incoming) {
		// 关键事件进来时：直接驱逐最旧事件（可能是普通或关键），保证实时性。
		b.removeAt(0)
		return
	}
	// 普通事件进来时：优先找保留区之外的非关键事件驱逐。
	reserve := defaultEventBufferSize / 25 // 5000 -> 200
	if reserve < 10 {
		reserve = 10
	}
	for i := 0; i < len(b.events)-reserve; i++ {
		if !isTerminalEvent(b.events[i]) {
			b.removeAt(i)
			return
		}
	}
	// 找不到可驱逐的非关键事件，回退到驱逐最旧事件。
	b.removeAt(0)
}

// removeAt 删除指定索引的事件并重建 index。
func (b *eventBuffer) removeAt(idx int) {
	oldest := b.events[idx]
	delete(b.index, oldest.EventID)
	b.events = append(b.events[:idx], b.events[idx+1:]...)
	for id, i := range b.index {
		if i > idx {
			b.index[id] = i - 1
		}
	}
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

// Hub 是 WebSocket 广播与客户端管理器。
type Hub struct {
	clients        map[*Client]bool
	register       chan *Client
	unregister     chan *Client
	broadcast      chan event.Event
	controlHandler ControlHandler
	mu             sync.RWMutex
	// eventBuf 缓存最近广播的事件，用于断连重连后的 replay。
	eventBuf *eventBuffer
	// done 关闭后 Run 的 goroutine 会退出，用于优雅关闭。
	done chan struct{}
	// runLoopDone 在 Run 退出时关闭；Shutdown 用它等待 Run 结束。
	runLoopDone chan struct{}
}

func NewHub() *Hub {
	return &Hub{
		clients:       make(map[*Client]bool),
		register:      make(chan *Client),
		unregister:    make(chan *Client),
		broadcast:     make(chan event.Event),
		eventBuf:      newEventBuffer(defaultEventBufferSize),
		done:          make(chan struct{}),
		runLoopDone:   make(chan struct{}),
	}
}

// Shutdown 优雅关闭 Hub：先关闭 done 信号让 Run 退出，再等待 ctx 指定时间让
// Run goroutine 处理完已入队事件后返回。
// 注意：Shutdown 不会主动关闭底层 websocket 连接；Server 关闭时连接会自行断开。
func (h *Hub) Shutdown(ctx context.Context) error {
	select {
	case <-h.done:
		// 已经关闭
		return nil
	default:
	}
	close(h.done)
	select {
	case <-h.runLoopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run 启动 Hub 事件循环；当 Shutdown 关闭 done 时退出，同时关闭 runLoopDone。
func (h *Hub) Run() {
	defer close(h.runLoopDone)
	for {
		select {
		case <-h.done:
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			log.Infof("ws", "Client connected: %s (total: %d)", client.ID, len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)
			}
			h.mu.Unlock()
			log.Infof("ws", "Client disconnected: %s (total: %d)", client.ID, len(h.clients))

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
			log.Errorf("ws", "WebSocket upgrade error: %v", err)
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
			log.Infof("ws", "Client %s: unparseable message: %s", c.ID, string(message))
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
				log.Errorf("ws", "writePump: marshal error: %v", err)
				continue
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Errorf("ws", "writePump: write error: %v", err)
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
