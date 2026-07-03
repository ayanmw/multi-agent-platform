package runtime

// Persistence is the interface for saving task/step/conversation records.
// Implementations include db.Persistence or in-memory stores for testing.
type Persistence interface {
	SaveTask(taskID string, userInput string, agentIDs []string) error
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