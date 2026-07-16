package runtime

import (
	"sync"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// contextSnapshotStore holds the latest context-window snapshot for running
// (or recently running) tasks in memory. It is intentionally an ephemeral
// cache: the platform does not keep long-term context history, only the
// current snapshot of the current task. When an Engine builds a snapshot in
// think(), it records it here so the HTTP API can serve it on demand without
// invoking the LLM.
//
// The store is a package-level singleton because snapshots are keyed by task
// ID and must outlive individual Engine references: the API handler does not
// hold an Engine pointer, but it still needs to read the live snapshot.
var contextSnapshotStore sync.Map

// RecordTaskContextSnapshot stores the latest snapshot for a task.
// It is called by Engine.think() right after building the snapshot and before
// sending it to the event bus.
func RecordTaskContextSnapshot(taskID string, snapshot llm.ContextWindowSnapshot) {
	if taskID == "" {
		return
	}
	contextSnapshotStore.Store(taskID, snapshot)
}

// GetTaskContextSnapshot returns the latest live snapshot for a task, if any.
// The second return value reports whether a snapshot was found. Callers that
// need persisted snapshots should fall back to db.QueryConversationsByTask
// when this returns false.
func GetTaskContextSnapshot(taskID string) (llm.ContextWindowSnapshot, bool) {
	if taskID == "" {
		return llm.ContextWindowSnapshot{}, false
	}
	if v, ok := contextSnapshotStore.Load(taskID); ok {
		if s, ok := v.(llm.ContextWindowSnapshot); ok {
			return s, true
		}
	}
	return llm.ContextWindowSnapshot{}, false
}

// DeleteTaskContextSnapshot removes a task's snapshot. It is exposed so that
// callers can prune finished tasks if memory pressure becomes a concern; the
// engine itself does not call it automatically.
func DeleteTaskContextSnapshot(taskID string) {
	if taskID == "" {
		return
	}
	contextSnapshotStore.Delete(taskID)
}
