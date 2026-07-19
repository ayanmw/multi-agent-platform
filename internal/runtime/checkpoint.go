// Package runtime — 用于跨崩溃进行任务持久化的 Checkpoint / Recovery 管理器。
//
// # 设计理由
//
// CheckpointManager 使任务在进程崩溃后能够恢复。在每次 ReAct loop 迭代结束
// 时，Engine 会保存一个 checkpoint，其中包含完整对话历史、step 序号、token
// 计数和任务进度。如果进程崩溃，可以加载该 checkpoint，从上次中断处继续
// 执行任务。
//
// # Checkpoint 格式
//
// 每个 checkpoint 是一个存放在 checkpoint 目录下的 JSON 文件。文件名是
// task ID 加上 ".checkpoint.json" 后缀。JSON 内容包括：
//   - task_id：任务标识符
//   - agent_id：agent 标识符
//   - step_idx：当前 ReAct loop 迭代号
//   - total_tokens：累计 token 使用量
//   - messages：完整对话历史（system、user、assistant、tool）
//   - progress：当前任务进度（可选）
//   - created_at：checkpoint 保存时间
//
// # 恢复
//
// RecoverFromCheckpoint 函数加载一个 checkpoint 并创建一个新的 Engine，
// 恢复对话历史和 step 计数器。Engine 会从保存的 step 序号继续 ReAct loop。
//
// # 并发
//
// CheckpointManager 可安全用于并发场景——每次 Save 都写入独立文件，且在
// 大多数操作系统上，小规模写入的文件 I/O 是原子的。不过，对同一 task ID
// 的并发保存会互相覆盖（last write wins），这正是 checkpointing 所期望
// 的行为。
//
// # 清理
//
// 任务成功完成后，应通过 Delete() 删除其 checkpoint。这能避免陈旧的
// checkpoint 不断累积。List() 方法可用于找出所有可用 checkpoint（例如用于
// 恢复 UI）。
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

// CheckpointManager 管理用于崩溃恢复的任务 checkpoint。
// 它将 checkpoint 作为 JSON 文件写入磁盘，并提供列举、加载和删除
// checkpoint 的方法。
//
// # 用法
//
//	cm := runtime.NewCheckpointManager("data/checkpoints")
//	// ... 创建 engine 后，传给 EngineConfig.CheckpointManager
//	// ... 任务完成后，调用 cm.Delete(taskID)
//	// ... 要列举可恢复任务，调用 cm.List()
type CheckpointManager struct {
	// CheckpointDir 是存放 checkpoint 文件的目录。
	// 若不存在会自动创建。
	CheckpointDir string
}

// Checkpoint 表示一个已保存的 engine 状态，可用于在崩溃后恢复任务。
// 它包含完整对话历史、step 序号、token 计数和可选的任务进度。
type Checkpoint struct {
	// TaskID 是唯一的任务标识符。
	TaskID string `json:"task_id"`

	// AgentID 是当时正在执行该任务的 agent。
	AgentID string `json:"agent_id"`

	// StepIdx 是当前 ReAct loop 迭代号（0-based）。
	StepIdx int `json:"step_idx"`

	// TotalTokens 是所有 LLM 调用累计的 token 使用量。
	TotalTokens int `json:"total_tokens"`

	// Messages 是完整对话历史（system、user、assistant、tool）。
	// 这是恢复 ReAct loop 所需的主要状态。
	Messages []llm.Message `json:"messages"`

	// Progress 是当前任务进度，若可用。
	// nil 表示该任务未配置进度跟踪。
	Progress *harness.TaskProgress `json:"progress,omitempty"`

	// CreatedAt 是 checkpoint 保存的时间。
	CreatedAt time.Time `json:"created_at"`
}

// NewCheckpointManager 创建一个新的 CheckpointManager，将 checkpoint 存放到
// 指定目录。若该目录不存在则会创建。
//
// 若 dir 为空，则使用 "data/checkpoints" 作为默认值。
func NewCheckpointManager(dir string) *CheckpointManager {
	if dir == "" {
		dir = "data/checkpoints"
	}

	// 确保 checkpoint 目录存在。
	if err := os.MkdirAll(dir, 0755); err != nil {
		// 记录错误但不失败——checkpointing 是可选的。
		// 若目录无法创建，Engine 会跳过 checkpointing，
		// Save 方法会返回错误。
		_ = err
	}

	return &CheckpointManager{
		CheckpointDir: dir,
	}
}

// Save 为指定 task 把一个 checkpoint 写入磁盘。该 checkpoint 以 JSON 文件
// 形式保存，文件名为 "{taskID}.checkpoint.json"，位于 checkpoint 目录下。
//
// 若 checkpoint 目录不存在，则会创建。若文件已存在，会被覆盖（last write
// wins）。
//
// 错误会返回给调用方；Engine 会记录日志但不会中断任务——checkpointing 是
// best-effort 操作。
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

	// 确保 checkpoint 目录存在。
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

// Load 从磁盘读取指定 task ID 的 checkpoint。
// 成功时返回 checkpoint 和 nil 错误；若 checkpoint 文件不存在或无法解析，
// 返回 nil 和一个错误。
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

// Delete 移除指定 task ID 的 checkpoint 文件。
// 任务成功完成后应调用此方法，以防陈旧 checkpoint 不断累积。
//
// 若 checkpoint 文件不存在，Delete 返回 nil（无错误）。
func (cm *CheckpointManager) Delete(taskID string) error {
	path := filepath.Join(cm.CheckpointDir, taskID+".checkpoint.json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // 已被删除
		}
		return fmt.Errorf("delete checkpoint: %w", err)
	}
	return nil
}

// List 返回所有可用 checkpoint 的 task ID。
// 恢复 UI 用它来展示哪些任务可以恢复。
//
// 返回的 task ID 按字母（文件名）排序。仅包含以 ".checkpoint.json" 结尾
// 的文件。
func (cm *CheckpointManager) List() ([]string, error) {
	dir := cm.CheckpointDir

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 还没有任何 checkpoint
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
			// 提取 task ID："task_xxx.checkpoint.json" -> "task_xxx"
			taskID := name[:len(name)-len(".checkpoint.json")]
			taskIDs = append(taskIDs, taskID)
		}
	}

	return taskIDs, nil
}

// Exists 返回 true 表示指定 task ID 的 checkpoint 存在。
func (cm *CheckpointManager) Exists(taskID string) bool {
	path := filepath.Join(cm.CheckpointDir, taskID+".checkpoint.json")
	_, err := os.Stat(path)
	return err == nil
}

// RecoverFromCheckpoint 从 checkpoint 创建一个新的 Engine，恢复对话历史、
// step 序号和 token 计数。该 engine 已就绪，可从中断处继续 ReAct loop。
//
// # 用法
//
//	cm := runtime.NewCheckpointManager("data/checkpoints")
//	cp, err := cm.Load("task_20260101120000")
//	if err != nil { ... }
//	engine := runtime.RecoverFromCheckpoint(cp, cfg, tools, bus, taskID)
//	// engine.Run(ctx, "") 会从保存的 stepIdx 继续
//
// 返回的 engine 拥有从 checkpoint 恢复的对话历史。
// system prompt 不会从 checkpoint 恢复——它来自 EngineConfig，允许调用方
// 在恢复时更改 system prompt。
//
// 恢复场景下 Run() 的 userInput 参数可为空（""）——当输入为空且最后一条
// 消息是 tool result（这是 loop 中途保存 checkpoint 后的常见情形）时，
// engine 会跳过追加新 user message 这一步。
func RecoverFromCheckpoint(cp *Checkpoint, cfg EngineConfig, tools *tool.Registry, bus EventBus, taskID string) *Engine {
	// 若 engine config 已设置 MaxSteps，则在其基础上累加。否则使用
	// checkpoint 的 stepIdx 作为起点。
	cfg.MaxSteps = cp.StepIdx + cfg.MaxSteps
	if cfg.MaxSteps < cp.StepIdx+1 {
		cfg.MaxSteps = cp.StepIdx + 10 // 至少再保留 10 步
	}

	engine := NewEngine(cfg, tools, bus, taskID)

	// 恢复对话历史、step 序号和 token 计数。
	engine.messages = cp.Messages
	engine.stepIdx = cp.StepIdx
	engine.totalTokens = cp.TotalTokens

	// 若可用，恢复任务进度。
	if cp.Progress != nil {
		engine.taskProgress = cp.Progress
	}

	return engine
}
