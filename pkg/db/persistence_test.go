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

// TestInsertAgentOptionsEquivalence 验证 InsertAgent(options) 与 InsertAgentLegacy
// 对相同字段集合写入后读出的记录一致（Phase 8-A）。
func TestInsertAgentOptionsEquivalence(t *testing.T) {
	freshDB(t)

	tools := []string{"run_shell", "read_file"}
	if err := InsertAgent(InsertAgentOptions{
		ID: "a_opts", Name: "Opts", Description: "d", SystemPrompt: "sp",
		Model: "m", Endpoint: "e", APIKey: "k",
		Temperature: 0.5, MaxTokens: 1024, Tools: tools, IsDefault: false,
	}); err != nil {
		t.Fatalf("InsertAgent: %v", err)
	}
	if err := InsertAgentLegacy("a_legacy", "Legacy", "d", "sp", "m", "e", "k",
		0.5, 1024, tools, false); err != nil {
		t.Fatalf("InsertAgentLegacy: %v", err)
	}

	opts, err := QueryAgentByID("a_opts")
	if err != nil {
		t.Fatalf("QueryAgentByID opts: %v", err)
	}
	legacy, err := QueryAgentByID("a_legacy")
	if err != nil {
		t.Fatalf("QueryAgentByID legacy: %v", err)
	}

	// 除 ID/Name 外，两者字段应一致。
	if opts.Temperature != legacy.Temperature || opts.MaxTokens != legacy.MaxTokens {
		t.Fatalf("temp/maxTokens mismatch: %+v vs %+v", opts, legacy)
	}
	if opts.Model != legacy.Model || opts.APIEndpoint != legacy.APIEndpoint || opts.APIKey != legacy.APIKey {
		t.Fatalf("model/endpoint/key mismatch: %+v vs %+v", opts, legacy)
	}
	if len(opts.Tools) != len(legacy.Tools) {
		t.Fatalf("tools length mismatch: %v vs %v", opts.Tools, legacy.Tools)
	}
}

// TestUpdateAgentOptions 验证 UpdateAgent(options) 能正确覆盖写入。
func TestUpdateAgentOptions(t *testing.T) {
	freshDB(t)

	if err := InsertAgent(InsertAgentOptions{
		ID: "u1", Name: "orig", Model: "m1", Temperature: 0.7, MaxTokens: 4096,
	}); err != nil {
		t.Fatalf("InsertAgent: %v", err)
	}
	if err := UpdateAgent(UpdateAgentOptions{
		ID: "u1", Name: "updated", Description: "new desc", SystemPrompt: "new sp",
		Model: "m2", Endpoint: "e2", APIKey: "k2",
		Temperature: 0.3, MaxTokens: 2048, Tools: []string{"run_shell"},
	}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}

	got, err := QueryAgentByID("u1")
	if err != nil {
		t.Fatalf("QueryAgentByID: %v", err)
	}
	if got.Name != "updated" || got.Model != "m2" || got.Temperature != 0.3 || got.MaxTokens != 2048 {
		t.Fatalf("update not applied: %+v", got)
	}
	if len(got.Tools) != 1 || got.Tools[0] != "run_shell" {
		t.Fatalf("tools not updated: %v", got.Tools)
	}
}
