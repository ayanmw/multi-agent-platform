package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ayanmw/multi-agent-platform/pkg/event"

	"github.com/gorilla/websocket"
	"github.com/ayanmw/multi-agent-platform/internal/observability"
)

// log 是 observability.DefaultLogger 的包级别别名，便于结构化日志埋点调用。
var log = observability.DefaultLogger

// ===========================================================================
// N3-04 (E7 并发安全与可扩展)：WS 广播背压与限流配置
// ===========================================================================
//
// 设计目标：WS 广播层在过载（事件洪泛 / 慢客户端）下必须优雅降级——
// 丢弃并计数，而非阻塞引擎关键路径或 OOM；同时保护广播循环不被单个
// 病态慢客户端无限拖累。所有阈值经 HubConfig 注入，避免硬编码魔法值（E9）。

// HubConfig 持有 WS Hub 的背压与限流参数（N3-04）。
// 每个字段都有安全默认值；NewHub 使用默认配置，NewHubWithConfig 用于从
// 环境变量 / 调用方注入生产调优值。
type HubConfig struct {
	// BroadcastBufferSize 是 hub.broadcast 入站通道的缓冲容量。
	// 作为全局摄入背压缓冲：生产者（SendEvent）在缓冲满时非阻塞丢弃并计数，
	// 而非无限阻塞 Run 消费 goroutine，避免引擎在 WS 拥塞时饿死。
	BroadcastBufferSize int
	// ClientSendBuffer 是每个 client.Send 通道的缓冲容量。
	// 吸收单个客户端的网络抖动，满后触发慢客户端丢弃 + 计数。
	ClientSendBuffer int
	// RateLimitPerSec 是全局摄入令牌桶的 refill 速率（事件/秒）。
	// 超过即限流丢弃 + 计数（ws_broadcast_rate_limited_total），防止单一生产者洪泛。
	RateLimitPerSec float64
	// RateLimitBurst 是全局摄入令牌桶的突发容量（允许瞬时峰值）。
	RateLimitBurst float64
	// SlowClientDropThreshold 是单个 client 连续投递失败（Send 缓冲满）达到该次数后
	// 被判定为慢客户端并主动注销，回收其资源、保护广播循环。
	SlowClientDropThreshold int
}

// DefaultHubConfig 返回生产安全的默认背压/限流参数。
// 默认值刻意宽松：正常负载远低于阈值，不会误伤；仅在真实洪泛/慢客户端时介入。
func DefaultHubConfig() HubConfig {
	return HubConfig{
		BroadcastBufferSize:     8192,
		ClientSendBuffer:        256,
		RateLimitPerSec:         10000,
		RateLimitBurst:          20000,
		SlowClientDropThreshold: 256,
	}
}

// HubConfigFromEnv 从环境变量读取背压/限流调优值，缺失或非法时回退默认值。
// 这样生产部署可在不重编译的情况下调整 WS 层容量（E9 配置化）。
func HubConfigFromEnv() HubConfig {
	c := DefaultHubConfig()
	if v := os.Getenv("WS_BROADCAST_BUFFER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.BroadcastBufferSize = n
		}
	}
	if v := os.Getenv("WS_CLIENT_SEND_BUFFER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.ClientSendBuffer = n
		}
	}
	if v := os.Getenv("WS_RATE_LIMIT_PER_SEC"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			c.RateLimitPerSec = f
		}
	}
	if v := os.Getenv("WS_RATE_LIMIT_BURST"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			c.RateLimitBurst = f
		}
	}
	if v := os.Getenv("WS_SLOW_CLIENT_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.SlowClientDropThreshold = n
		}
	}
	return c
}

// tokenBucket 是一个基于互斥锁保护的简单令牌桶限流器（N3-04 摄入限流）。
// 与 golang.org/x/time/rate 不同，这里仅需极简语义（allow / 非阻塞），自实现
// 避免引入额外依赖；allow 不等待，调用方据此决定是否丢弃事件。
type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	capacity float64
	rate     float64 // 令牌 refill 速率（个/秒）
	last     time.Time
}

// newTokenBucket 构造一个容量为 burst、refill 速率为 rate 个/秒的令牌桶。
func newTokenBucket(rate, burst float64) *tokenBucket {
	return &tokenBucket{
		tokens:   burst,
		capacity: burst,
		rate:     rate,
		last:     time.Now(),
	}
}

// allow 尝试消费一个令牌；成功返回 true，令牌不足返回 false（调用方应丢弃）。
// 基于经过的时间补充令牌，上限为容量；线程安全（互斥锁保护）。
func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

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
	// sessionIDs 是该 client 订阅的 session 过滤集（E3 隔离增强，N3-02）。
	// 为空表示「接收全部」—— 向后兼容旧前端全局连接（不过滤）；
	// 非空时仅投递 session_id 命中该集的事件，实现服务端会话级事件隔离，
	// 杜绝任意 WS 连接收到其它 session 的实时事件（跨 session 数据泄漏）。
	sessionIDs []string
	// dropCount 是慢客户端连续投递失败（client.Send 缓冲满）的累计计数，
	// 仅在 Hub.Run 广播循环内、持有 h.mu 时访问（单 goroutine 写），用于判断是否
	// 达到 SlowClientDropThreshold 触发主动注销。成功投递时由 resetDropCount 清零。
	dropCount int
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
	// cfg 是背压/限流配置（N3-04），由 NewHubWithConfig 注入。
	cfg HubConfig
	// limiter 是全局摄入令牌桶（N3-04），用于 WS 广播摄入限流。
	limiter *tokenBucket
}

// NewHub 返回一个使用默认背压/限流配置的 Hub（N3-04）。
// 默认配置宽松，正常负载不会触发丢弃；仅在真实洪泛/慢客户端时介入。
func NewHub() *Hub {
	return NewHubWithConfig(DefaultHubConfig())
}

// NewHubWithConfig 返回使用指定背压/限流配置的 Hub（N3-04）。
// 零值或非法字段回退为 DefaultHubConfig 的安全默认值。
func NewHubWithConfig(cfg HubConfig) *Hub {
	if cfg.BroadcastBufferSize <= 0 {
		cfg.BroadcastBufferSize = 8192
	}
	if cfg.ClientSendBuffer <= 0 {
		cfg.ClientSendBuffer = 256
	}
	if cfg.RateLimitPerSec <= 0 {
		cfg.RateLimitPerSec = 10000
	}
	if cfg.RateLimitBurst <= 0 {
		cfg.RateLimitBurst = 20000
	}
	if cfg.SlowClientDropThreshold <= 0 {
		cfg.SlowClientDropThreshold = 256
	}
	return &Hub{
		clients:       make(map[*Client]bool),
		register:      make(chan *Client, 64),
		unregister:    make(chan *Client, 64),
		broadcast:     make(chan event.Event, cfg.BroadcastBufferSize),
		eventBuf:      newEventBuffer(defaultEventBufferSize),
		done:          make(chan struct{}),
		runLoopDone:   make(chan struct{}),
		cfg:           cfg,
		limiter:       newTokenBucket(cfg.RateLimitPerSec, cfg.RateLimitBurst),
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
				// E3 隔离增强（N3-02）：按 client 订阅的 session 集过滤，
				// 未订阅者（legacy）接收全部，已订阅者仅收命中事件。
				if !client.clientAcceptsEvent(evt) {
					continue
				}
				// N3-04 (E7) 背压 · 慢客户端：非阻塞投递，Send 缓冲满即丢弃并计数；
				// 连续丢弃累计达阈值则判定为慢客户端，主动注销回收资源、保护广播循环。
				select {
				case client.Send <- evt:
					client.resetDropCount()
				default:
					observability.DefaultMetrics.IncrWSClientSendDrops()
					if client.incrDropCount() >= h.cfg.SlowClientDropThreshold {
						client.evictSlow()
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) SendEvent(evt event.Event) {
	// N2-01：白盒闭合哨兵——任何事件广播前先过完整性校验。
	// 校验未通过的事件仍照常广播（不丢数据），但必须计入 malformed 哨兵并告警，
	// 便于排障；绝不能让非法事件静默下发到前端或写库。
	observability.DefaultMetrics.IncrEventsTotal()
	if issues := event.Validate(evt); len(issues) > 0 {
		observability.DefaultMetrics.IncrMalformedEvents()
		log.Warnf("ws", "malformed event passed validation gate type=%s event_id=%s issues=%v",
			evt.Type, evt.EventID, issues)
	}
	// N2-01：按 agent / session / step 维度累加指标（单一漏斗，覆盖全部事件源）。
	recordEventMetrics(evt)

	// N3-04 (E7) 背压 · 摄入限流：全局令牌桶防止单一生产者洪泛 WS 层。
	// 超过速率即限流丢弃并计数（ws_broadcast_rate_limited_total），而非阻塞引擎关键路径；
	// 正常负载远低于阈值，不会误伤。
	if !h.limiter.allow() {
		observability.DefaultMetrics.IncrWSBroadcastRateLimited()
		log.Warnf("ws", "broadcast rate limit exceeded, dropping event type=%s event_id=%s", evt.Type, evt.EventID)
		return
	}

	// N3-04 (E7) 背压 · 摄入缓冲：broadcast 入站通道有界；缓冲满时非阻塞丢弃 + 计数
	// （ws_broadcast_drops_total），避免 SendEvent 调用方（引擎/编排等 goroutine）在
	// WS 拥塞时无限阻塞或 OOM。即便 Hub 已 Shutdown（Run 退出、无人消费），本分支也
	// 仅会填满缓冲后丢弃，SendEvent 绝不阻塞——这是相较于「无缓冲阻塞」的关机安全性改进。
	select {
	case h.broadcast <- evt:
	default:
		observability.DefaultMetrics.IncrWSBroadcastDrops()
		log.Warnf("ws", "broadcast buffer full, dropping event type=%s event_id=%s", evt.Type, evt.EventID)
	}
}

// recordEventMetrics 从事件中抽取 agent / session / step 维度并累加到指标收集器。
// 仅在事件类型映射到受控维度时计数，避免无意义的基数膨胀。
func recordEventMetrics(evt event.Event) {
	switch evt.Type {
	case "task_started":
		observability.DefaultMetrics.RecordAgentTask(evt.AgentID, "started")
		if sid, ok := evt.Data["session_id"].(string); ok && sid != "" {
			observability.DefaultMetrics.RecordSessionTask(sid, "started")
		}
	case "task_completed":
		observability.DefaultMetrics.RecordAgentTask(evt.AgentID, "completed")
	case "task_failed":
		observability.DefaultMetrics.RecordAgentTask(evt.AgentID, "failed")
	case "session_status":
		// runner.go 在会话终态时广播 session_status{status}；仅记录终态以不重复计数。
		if sid, ok := evt.Data["session_id"].(string); ok && sid != "" {
			if st, ok := evt.Data["status"].(string); ok {
				switch st {
				case "completed":
					observability.DefaultMetrics.RecordSessionTask(sid, "completed")
				case "failed":
					observability.DefaultMetrics.RecordSessionTask(sid, "failed")
				}
			}
		}
	case "step_started":
		if st, ok := evt.Data["type"].(string); ok && st != "" {
			observability.DefaultMetrics.RecordAgentStep(evt.AgentID, st)
		}
	}
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
		Send: make(chan event.Event, h.cfg.ClientSendBuffer),
	}
	h.register <- c
	return c
}

// RegisterTestClientWithSessions 注册一个带 session 订阅过滤的测试 client
// （E3 隔离增强，N3-02）。sessions 非空时该 client 仅接收 session_id 命中
// 的事件；为空时接收全部（legacy 行为）。仅用于测试。
func (h *Hub) RegisterTestClientWithSessions(id string, sessions []string) *Client {
	c := &Client{
		ID:         id,
		Hub:        h,
		Send:       make(chan event.Event, h.cfg.ClientSendBuffer),
		sessionIDs: sessions,
	}
	h.register <- c
	return c
}

// UnregisterTestClient 注销一个测试 client。仅用于测试。
func (h *Hub) UnregisterTestClient(c *Client) {
	h.unregister <- c
}

// clientCount 返回当前已注册客户端数量（仅测试断言用，受 h.mu 保护）。
func (h *Hub) clientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// incrDropCount 累加该 client 的连续投递失败计数并返回新值（仅广播循环内调用）。
func (c *Client) incrDropCount() int {
	c.dropCount++
	return c.dropCount
}

// resetDropCount 在成功投递后清零连续失败计数（仅广播循环内调用）。
func (c *Client) resetDropCount() {
	c.dropCount = 0
}

// evictSlow 标记该 client 为慢客户端并请求 Hub 注销回收资源。
// unregister 通道已缓冲，此处发送绝不阻塞广播循环（避免控制通道死锁）。
func (c *Client) evictSlow() {
	log.Warnf("ws", "slow client evicted after sustained send drops client=%s drops=%d", c.ID, c.dropCount)
	c.Hub.unregister <- c
	c.dropCount = 0
}

func ServeWS(hub *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Errorf("ws", "WebSocket upgrade error: %v", err)
			return
		}

		// E3 隔离增强（N3-02）：从 ?session_id= 读取订阅集（逗号分隔）。
		// 不传则 sessionIDs 为空 → 接收全部（legacy 行为，兼容旧前端全局连接）。
		// 传参后该连接仅收到命中 session 的事件，实现服务端会话级事件隔离。
		client := &Client{
			ID:         generateID(),
			Hub:        hub,
			Send:       make(chan event.Event, hub.cfg.ClientSendBuffer),
			Conn:       conn,
			sessionIDs: parseSessionFilter(r.URL.Query().Get("session_id")),
		}

		hub.register <- client
		go client.writePump()
		go client.readPump()
	}
}

// parseSessionFilter 将逗号分隔的 session_id 查询参数解析为订阅集。
// 空值或仅空白返回 nil（表示接收全部，legacy 行为）。
func parseSessionFilter(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// eventSessionID 从事件 Data 中提取 session_id（用于服务端按会话过滤广播）。
// 事件未携带 Data 或 session_id 时返回空串。
func eventSessionID(evt event.Event) string {
	if evt.Data == nil {
		return ""
	}
	if sid, ok := evt.Data["session_id"].(string); ok {
		return sid
	}
	return ""
}

// clientAcceptsEvent 判断 client 是否应接收该事件：
//   - 未订阅任何 session（空 filter）→ 接收全部（legacy 行为）。
//   - 已订阅 → 仅当事件携带非空 session_id 且命中订阅集时接收；
//     无 session_id 的系统事件只投递给未订阅的 legacy client。
func (c *Client) clientAcceptsEvent(evt event.Event) bool {
	if len(c.sessionIDs) == 0 {
		return true
	}
	sid := eventSessionID(evt)
	if sid == "" {
		return false
	}
	for _, s := range c.sessionIDs {
		if s == sid {
			return true
		}
	}
	return false
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
