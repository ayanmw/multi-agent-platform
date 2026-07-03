package event

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Event represents a structured event sent over WebSocket
type Event struct {
	EventID   string                 `json:"event_id"`
	TaskID    string                 `json:"task_id"`
	AgentID   string                 `json:"agent_id"`
	StepIndex int                    `json:"step_index"`
	Type      string                 `json:"type"`
	Timestamp int64                  `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
}

// NewEvent creates a new event with auto-generated ID and timestamp
func NewEvent(eventType, taskID, agentID string, stepIndex int, data map[string]interface{}) Event {
	if data == nil {
		data = make(map[string]interface{})
	}
	return Event{
		EventID:   generateID(),
		TaskID:    taskID,
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
