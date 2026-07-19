package runtime

import (
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

func TestRecordAndGetSnapshot(t *testing.T) {
	taskID := "task_snapshot_001"
	// 清理一下，避免与并行测试相互干扰。
	DeleteTaskContextSnapshot(taskID)
	defer DeleteTaskContextSnapshot(taskID)

	if _, ok := GetTaskContextSnapshot(taskID); ok {
		t.Fatalf("expected no snapshot before recording")
	}

	snapshot := llm.ContextWindowSnapshot{
		Model:                "deepseek-v4-flash",
		MaxContextTokens:     128000,
		EstimatedTotalTokens: 42,
		EstimatedUsageRatio:  0.000328,
		Messages: []llm.ContextSnapshotMessage{
			{Role: "system", Content: "You are helpful", EstimatedTokens: 10},
			{Role: "user", Content: "hi", EstimatedTokens: 5},
		},
	}

	RecordTaskContextSnapshot(taskID, snapshot)

	got, ok := GetTaskContextSnapshot(taskID)
	if !ok {
		t.Fatalf("expected snapshot after recording")
	}
	if got.Model != snapshot.Model {
		t.Fatalf("model mismatch: got %q, want %q", got.Model, snapshot.Model)
	}
	if got.EstimatedTotalTokens != snapshot.EstimatedTotalTokens {
		t.Fatalf("tokens mismatch: got %d, want %d", got.EstimatedTotalTokens, snapshot.EstimatedTotalTokens)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len mismatch: got %d, want 2", len(got.Messages))
	}
}

func TestGetSnapshotEmptyTaskID(t *testing.T) {
	RecordTaskContextSnapshot("", llm.ContextWindowSnapshot{Model: "x"})
	if _, ok := GetTaskContextSnapshot(""); ok {
		t.Fatalf("expected false for empty task ID")
	}
}

func TestDeleteSnapshot(t *testing.T) {
	taskID := "task_snapshot_delete"
	RecordTaskContextSnapshot(taskID, llm.ContextWindowSnapshot{Model: "x"})
	if _, ok := GetTaskContextSnapshot(taskID); !ok {
		t.Fatalf("expected snapshot before deletion")
	}
	DeleteTaskContextSnapshot(taskID)
	if _, ok := GetTaskContextSnapshot(taskID); ok {
		t.Fatalf("expected snapshot to be deleted")
	}
}

func TestLatestSnapshotWins(t *testing.T) {
	taskID := "task_snapshot_overwrite"
	defer DeleteTaskContextSnapshot(taskID)

	RecordTaskContextSnapshot(taskID, llm.ContextWindowSnapshot{EstimatedTotalTokens: 10})
	RecordTaskContextSnapshot(taskID, llm.ContextWindowSnapshot{EstimatedTotalTokens: 20})

	got, _ := GetTaskContextSnapshot(taskID)
	if got.EstimatedTotalTokens != 20 {
		t.Fatalf("expected latest snapshot to win, got %d", got.EstimatedTotalTokens)
	}
}
