package runtime

// Persistence 是保存 task/step/conversation 记录的 interface。
// 实现包括 db.Persistence 或测试用的内存存储。
type Persistence interface {
	SaveTask(taskID string, userInput string, agentIDs []string) error
	SaveTaskMeta(taskID string, sessionID string, parentTaskID string, isRoot bool) error
	UpdateTask(taskID string, status string, finalResult string, totalTokens int) error
	UpdateTaskDuration(taskID string, durationMs int) error
	SaveStep(step StepRecord) error
	SaveConversation(conv ConversationRecord) error
	// QueryTaskSessionID 返回某个 task 的 session_id；若该 task 未知或
	// 持久化不可用，则返回空字符串。这是一个只读 helper，供只持有 root task ID
	// 的编排层使用，以便把 session ID 传播给子任务。
	QueryTaskSessionID(taskID string) string
	// SaveAgentMessage 持久化一条经 AgentBus 路由的 agent 间消息。
	// TaskID 为必填，便于后续通过 GET /api/tasks/:id/agent-messages 取回。
	SaveAgentMessage(msg AgentBusMessage) error
	// LoadAgentMessages 返回某个 task 的所有 AgentBus 消息，按时间从旧到新
	// 排序。若该 task 没有消息，返回空 slice（而非 nil）。
	LoadAgentMessages(taskID string) ([]AgentBusMessage, error)
}

// StepRecord 是待持久化的一个 step
type StepRecord struct {
	TaskID     string
	AgentID    string
	StepIndex  int
	Type       string
	Status     string
	Content    string
	ToolName   string
	ToolInput  map[string]any
	ToolOutput string
	DurationMs int
	TokenUsed  int
}

// ConversationRecord 是待持久化的一条对话消息
type ConversationRecord struct {
	TaskID  string
	Role    string
	Content string
}

// SessionMessageRecord 是待持久化到 session_messages 表的消息。
// 它被 Engine 的 SessionMessageWriter 回调使用，把 ReAct loop 期间产生的
// 每条消息（system/user/assistant/tool）写入 session_messages 表，从而支持
// 多轮对话历史。
type SessionMessageRecord struct {
	TaskID     string // 该消息所属的 task
	TurnIndex  int    // 会话内的 turn 序号（0-based）
	Role       string // system、user、assistant 或 tool
	Content    string // 消息内容
	ToolCallID string // tool role 消息的 tool call ID
	ToolCalls  string // assistant 消息的 JSON 序列化 tool calls
	TokenCount int    // 该消息的 token 计数（仅 assistant 消息）
}

// AgentBusMessage 是 AgentBus agent 间消息在持久化层的表示。
// 它有意与 AgentMessage（runtime 内存中的类型）区分开，以避免持久化存储
// 细节（时间戳、metadata 序列化、未来 schema 列）泄漏到 Engine 的 API 表面。
//
// TaskID 为必填：当前端请求 GET /api/tasks/:id/agent-messages 时，持久化
// 以它作为主查询键。
type AgentBusMessage struct {
	TaskID        string
	FromAgentID   string
	ToAgentID     string
	SubTaskID     string // Phase 7-I: 支持按子任务路由
	FromSubTaskID string // Phase 7-J: 发送方子任务
	Type          string
	Content       string
	Metadata      map[string]string
}
