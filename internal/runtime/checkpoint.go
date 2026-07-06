// Package runtime — Checkpoint / Recovery manager for task persistence across crashes.
//
// # Design Rationale
//
// The CheckpointManager enables task recovery after process crashes. At the end of
// each ReAct loop iteration, the Engine saves a checkpoint containing the full
// conversation history, step index, token count, and task progress. If the process
// crashes, the checkpoint can be loaded and the task can be resumed from where it
// left off.
//
// # Checkpoint Format
//
// Each checkpoint is a JSON file stored in the checkpoint directory. The filename
// is the task ID with a ".checkpoint.json" suffix. The JSON contains:
//   - task_id: the task identifier
//   - agent_id: the agent identifier
//   - step_idx: the current ReAct loop iteration
//   - total_tokens: cumulative token usage
//   - messages: the full conversation history (system, user, assistant, tool)
//   - progress: the current task progress (optional)
//   - created_at: when the checkpoint was saved
//
// # Recovery
//
// The RecoverFromCheckpoint function loads a checkpoint and creates a new Engine
// with the restored conversation history and step counter. The engine continues
// the ReAct loop from the saved step index.
//
// # Concurrency
//
// The CheckpointManager is safe for concurrent use — each call to Save writes a
// separate file, and file I/O on most operating systems is atomic for small writes.
// However, concurrent saves for the same task ID will overwrite each other (last
// write wins), which is the desired behavior for checkpointing.
//
// # Cleanup
//
// After a task completes successfully, the checkpoint should be deleted via
// Delete(). This prevents stale checkpoints from accumulating. The List() method
// can be used to find all available checkpoints (e.g., for a recovery UI).
package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

// CheckpointManager manages task checkpoints for crash recovery.
// It writes checkpoints to disk as JSON files and provides methods for
// listing, loading, and deleting checkpoints.
//
// # Usage
//
//	cm := runtime.NewCheckpointManager("data/checkpoints")
//	// ... after engine creation, pass to EngineConfig.CheckpointManager
//	// ... after task completion, call cm.Delete(taskID)
//	// ... to list recoverable tasks, call cm.List()
type CheckpointManager struct {
	// CheckpointDir is the directory where checkpoint files are stored.
	// It is created automatically if it does not exist.
	CheckpointDir string
}

// Checkpoint represents a saved engine state that can be used to resume a task
// after a crash. It contains the full conversation history, step index, token
// count, and optional task progress.
type Checkpoint struct {
	// TaskID is the unique task identifier.
	TaskID string `json:"task_id"`

	// AgentID is the agent that was executing the task.
	AgentID string `json:"agent_id"`

	// StepIdx is the current ReAct loop iteration (0-based).
	StepIdx int `json:"step_idx"`

	// TotalTokens is the cumulative token usage across all LLM calls.
	TotalTokens int `json:"total_tokens"`

	// Messages is the full conversation history (system, user, assistant, tool).
	// This is the primary state needed to resume the ReAct loop.
	Messages []llm.Message `json:"messages"`

	// Progress is the current task progress, if available.
	// nil means no progress tracking was configured for this task.
	Progress *harness.TaskProgress `json:"progress,omitempty"`

	// CreatedAt is when the checkpoint was saved.
	CreatedAt time.Time `json:"created_at"`
}

// NewCheckpointManager creates a new CheckpointManager that stores checkpoints
// in the given directory. The directory is created if it does not exist.
//
// If dir is empty, "data/checkpoints" is used as the default.
func NewCheckpointManager(dir string) *CheckpointManager {
	if dir == "" {
		dir = "data/checkpoints"
	}

	// Ensure the checkpoint directory exists.
	if err := os.MkdirAll(dir, 0755); err != nil {
		// Log the error but don't fail — checkpointing is optional.
		// The Engine will skip checkpointing if the directory can't be created
		// and the Save method will return an error.
		_ = err
	}

	return &CheckpointManager{
		CheckpointDir: dir,
	}
}

// Save writes a checkpoint to disk for the given task. The checkpoint is saved
// as a JSON file named "{taskID}.checkpoint.json" in the checkpoint directory.
//
// If the checkpoint directory does not exist, it is created. If the file already
// exists, it is overwritten (last write wins).
//
// Errors are returned to the caller; the Engine logs them but does not abort
// the task — checkpointing is a best-effort operation.
func (cm *CheckpointManager) Save(taskID, agentID string, stepIdx, totalTokens int, messages []llm.Message, progress *harness.TaskProgress) error {
	cp := Checkpoint{
		TaskID:      taskID,
		AgentID:     agentID,
		StepIdx:     stepIdx,
		TotalTokens: totalTokens,
		Messages:    messages,
		Progress:    progress,
		CreatedAt:   time.Now(),
	}

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	// Ensure the checkpoint directory exists.
	dir := cm.CheckpointDir
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create checkpoint dir: %w", err)
	}

	path := filepath.Join(dir, taskID+".checkpoint.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}

	return nil
}

// Load reads a checkpoint from disk for the given task ID.
// Returns the checkpoint and nil error on success, or nil and an error if the
// checkpoint file does not exist or cannot be parsed.
func (cm *CheckpointManager) Load(taskID string) (*Checkpoint, error) {
	path := filepath.Join(cm.CheckpointDir, taskID+".checkpoint.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}

	return &cp, nil
}

// Delete removes the checkpoint file for the given task ID.
// This should be called after a task completes successfully to prevent stale
// checkpoints from accumulating.
//
// If the checkpoint file does not exist, Delete returns nil (no error).
func (cm *CheckpointManager) Delete(taskID string) error {
	path := filepath.Join(cm.CheckpointDir, taskID+".checkpoint.json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // already deleted
		}
		return fmt.Errorf("delete checkpoint: %w", err)
	}
	return nil
}

// List returns the task IDs of all available checkpoints.
// This is used by the recovery UI to show which tasks can be resumed.
//
// The returned task IDs are sorted alphabetically (by filename). Only files
// ending with ".checkpoint.json" are included.
func (cm *CheckpointManager) List() ([]string, error) {
	dir := cm.CheckpointDir

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no checkpoints yet
		}
		return nil, fmt.Errorf("read checkpoint dir: %w", err)
	}

	var taskIDs []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) == ".json" && filepath.Ext(name[:len(name)-5]) == ".checkpoint" {
			// Extract task ID: "task_xxx.checkpoint.json" -> "task_xxx"
			taskID := name[:len(name)-len(".checkpoint.json")]
			taskIDs = append(taskIDs, taskID)
		}
	}

	return taskIDs, nil
}

// Exists returns true if a checkpoint exists for the given task ID.
func (cm *CheckpointManager) Exists(taskID string) bool {
	path := filepath.Join(cm.CheckpointDir, taskID+".checkpoint.json")
	_, err := os.Stat(path)
	return err == nil
}

// RecoverFromCheckpoint creates a new Engine from a checkpoint, restoring the
// conversation history, step index, and token count. The engine is ready to
// continue the ReAct loop from where it left off.
//
// # Usage
//
//	cm := runtime.NewCheckpointManager("data/checkpoints")
//	cp, err := cm.Load("task_20260101120000")
//	if err != nil { ... }
//	engine := runtime.RecoverFromCheckpoint(cp, cfg, tools, bus, taskID)
//	// engine.Run(ctx, "") will continue from the saved stepIdx
//
// The returned engine has the conversation history restored from the checkpoint.
// The system prompt is NOT restored from the checkpoint — it comes from the
// EngineConfig, allowing the caller to change the system prompt for recovery.
//
// The userInput parameter to Run() can be empty ("") for recovery — the engine
// will skip appending a new user message if the input is empty and the last
// message was a tool result (which is the normal case after a checkpoint saved
// mid-loop).
func RecoverFromCheckpoint(cp *Checkpoint, cfg EngineConfig, tools *tool.Registry, bus EventBus, taskID string) *Engine {
	// If the engine config has Already set MaxSteps, prefer it. Otherwise, use the
	// checkpoint's stepIdx as a starting point.
	cfg.MaxSteps = cp.StepIdx + cfg.MaxSteps
	if cfg.MaxSteps < cp.StepIdx+1 {
		cfg.MaxSteps = cp.StepIdx + 10 // ensure at least 10 more steps
	}

	engine := NewEngine(cfg, tools, bus, taskID)

	// Restore the conversation history, step index, and token count.
	engine.messages = cp.Messages
	engine.stepIdx = cp.StepIdx
	engine.totalTokens = cp.TotalTokens

	// Restore task progress if available.
	if cp.Progress != nil {
		engine.taskProgress = cp.Progress
	}

	return engine
}