package db

import (
	"testing"
)

// TestAgentBusMessageSubTaskIDRoundTrip 验证 AgentBusMessage 的 SubTaskID
// 与 FromSubTaskID 经 InsertAgentMessage / QueryAgentMessages 往返后保持不变。
// Phase 7-J。
func TestAgentBusMessageSubTaskIDRoundTrip(t *testing.T) {
	freshDB(t)

	if err := InsertTask(TaskRecord{ID: "task_abc", UserInput: "test", Status: "running"}); err != nil {
		t.Fatalf("InsertTask: %v", err)
	}

	msg := AgentBusMessage{
		TaskID:        "task_abc",
		SubTaskID:     "task_abc_sub1",
		FromSubTaskID: "task_abc_sub2",
		FromAgentID:   "agent_a",
		ToAgentID:     "agent_b",
		Type:          "observation",
		Content:       "hello",
		Metadata: map[string]string{
			"task_id":          "task_abc",
			"from_sub_task_id": "task_abc_sub2",
		},
	}
	if err := InsertAgentMessage(msg); err != nil {
		t.Fatalf("InsertAgentMessage: %v", err)
	}

	rows, err := QueryAgentMessages("task_abc")
	if err != nil {
		t.Fatalf("QueryAgentMessages: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 message, got %d", len(rows))
	}
	got := rows[0]
	if got.TaskID != "task_abc" {
		t.Errorf("TaskID = %q, want task_abc", got.TaskID)
	}
	if got.SubTaskID != "task_abc_sub1" {
		t.Errorf("SubTaskID = %q, want task_abc_sub1", got.SubTaskID)
	}
	if got.FromSubTaskID != "task_abc_sub2" {
		t.Errorf("FromSubTaskID = %q, want task_abc_sub2", got.FromSubTaskID)
	}
	if got.FromAgentID != "agent_a" {
		t.Errorf("FromAgentID = %q, want agent_a", got.FromAgentID)
	}
	if got.ToAgentID != "agent_b" {
		t.Errorf("ToAgentID = %q, want agent_b", got.ToAgentID)
	}
	if got.Type != "observation" {
		t.Errorf("Type = %q, want observation", got.Type)
	}
	if got.Content != "hello" {
		t.Errorf("Content = %q, want hello", got.Content)
	}
	if got.Metadata["from_sub_task_id"] != "task_abc_sub2" {
		t.Errorf("metadata from_sub_task_id = %q, want task_abc_sub2", got.Metadata["from_sub_task_id"])
	}
}
