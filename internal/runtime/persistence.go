package runtime

// Persistence is the interface for saving task/step/conversation records.
// Implementations include db.Persistence or in-memory stores for testing.
type Persistence interface {
	SaveTask(taskID string, userInput string, agentIDs []string) error
	SaveTaskMeta(taskID string, sessionID string, parentTaskID string, isRoot bool) error
	UpdateTask(taskID string, status string, finalResult string, totalTokens int) error
	UpdateTaskDuration(taskID string, durationMs int) error
	SaveStep(step StepRecord) error
	SaveConversation(conv ConversationRecord) error
	// QueryTaskSessionID returns the session_id for a task, or empty string if
	// the task is not known or persistence is unavailable. It is a read-only
	// helper used by orchestration layers that only hold a root task ID and
	// need to propagate the session ID to child tasks.
	QueryTaskSessionID(taskID string) string
	// SaveAgentMessage persists a single inter-agent message routed through
	// the AgentBus. The TaskID is required so the message can later be
	// retrieved by GET /api/tasks/:id/agent-messages.
	SaveAgentMessage(msg AgentBusMessage) error
	// LoadAgentMessages returns every AgentBus message for a task, ordered
	// oldest first. Returns an empty slice (not nil) if the task has no
	// messages.
	LoadAgentMessages(taskID string) ([]AgentBusMessage, error)
}

// StepRecord is a step to be persisted
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

// ConversationRecord is a conversation message to be persisted
type ConversationRecord struct {
	TaskID  string
	Role    string
	Content string
}

// SessionMessageRecord is a message to be persisted to the session_messages table.
// It is used by the Engine's SessionMessageWriter callback to write every message
// (system/user/assistant/tool) generated during the ReAct loop to the session_messages
// table, enabling multi-turn conversation history.
type SessionMessageRecord struct {
	TaskID     string // the task this message belongs to
	TurnIndex  int    // the turn index within the session (0-based)
	Role       string // system, user, assistant, or tool
	Content    string // the message content
	ToolCallID string // the tool call ID (for tool role messages)
	ToolCalls  string // JSON-serialized tool calls (for assistant messages)
	TokenCount int    // token count for this message (assistant messages only)
}

// AgentBusMessage is the persistence-layer representation of an AgentBus
// inter-agent message. It is intentionally distinct from AgentMessage (the
// runtime/in-memory type) so that persistence storage details (timestamp,
// metadata serialisation, future schema columns) don't leak into the
// engine's API surface.
//
// TaskID is mandatory: persistence uses it as the primary lookup key when
// the frontend requests GET /api/tasks/:id/agent-messages.
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