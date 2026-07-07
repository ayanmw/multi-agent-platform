package runtime

// Persistence is the interface for saving task/step/conversation records.
// Implementations include db.Persistence or in-memory stores for testing.
type Persistence interface {
	SaveTask(taskID string, userInput string, agentIDs []string) error
	SaveTaskMeta(taskID string, sessionID string, parentTaskID string, isRoot bool) error
	UpdateTask(taskID string, status string, finalResult string, totalTokens int) error
	SaveStep(step StepRecord) error
	SaveConversation(conv ConversationRecord) error
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