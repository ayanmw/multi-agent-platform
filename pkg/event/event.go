package event

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Memory lifecycle events broadcast over WebSocket. These are produced by the
// Memory CRUD API handlers so the frontend can keep its cache in sync.
const (
	EventMemoryCreated         = "memory_created"
	EventMemoryUpdated         = "memory_updated"
	EventMemoryDeleted         = "memory_deleted"
	EventMemoryPromoted        = "memory_promoted"
	EventMemoryRecallDone      = "memory_recall_performed"
	EventHeartbeatBeat         = "heartbeat_beat"
	EventContextWindowSnapshot = "context_window_snapshot"

	// EventTaskEvaluated is emitted after a task completes when the engine has
	// run the AcceptanceEvaluator against the case contract. It carries passed,
	// score, reason, and the full evaluation report.
	EventTaskEvaluated = "task_evaluated"
)

// Event represents a structured event sent over WebSocket
type Event struct {
	EventID    string         `json:"event_id"`
	TaskID     string         `json:"task_id"`
	SubTaskID  string         `json:"sub_task_id"`
	AgentID    string         `json:"agent_id"`
	StepIndex  int            `json:"step_index"`
	Type       string         `json:"type"`
	Timestamp  int64          `json:"timestamp"`
	Data       map[string]any `json:"data"`
}

// NewEvent creates a new event with auto-generated ID and timestamp
func NewEvent(eventType, taskID, agentID string, stepIndex int, data map[string]any) Event {
	return NewEventWithSubTask(eventType, taskID, "", agentID, stepIndex, data)
}

// NewEventWithSubTask creates a new event that carries explicit sub-task identity.
// taskID is the root task; subTaskID identifies the concrete agent execution instance.
// For the leader agent, subTaskID equals taskID; child agents have their own subTaskID.
func NewEventWithSubTask(eventType, taskID, subTaskID, agentID string, stepIndex int, data map[string]any) Event {
	if data == nil {
		data = make(map[string]any)
	}
	return Event{
		EventID:   generateID(),
		TaskID:    taskID,
		SubTaskID: subTaskID,
		AgentID:   agentID,
		StepIndex: stepIndex,
		Type:      eventType,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}
}

func generateID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return time.Now().Format("20060102150405")
	}
	return hex.EncodeToString(bytes)
}
