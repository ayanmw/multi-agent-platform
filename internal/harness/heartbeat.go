// Package harness —— Heartbeat：用于 Memory consolidation 的周期性后台任务。
//
// Heartbeat 按可配置间隔（默认 5 分钟）运行。每次 beat 会扫描新完成的任务，从其对话
// 历史生成 episode summary，并将候选 memory 写入 memories 表的 consolidated episodic
// tier。
//
// # 架构
//
// Heartbeat 是 Raw Episodic memory（conversations 表）与 Consolidated Episodic memory
// （memories 表，tier=consolidated）之间的桥梁。它实现 4 层 memory 系统的第 3 层：
//
//  1. Working Memory      —— 任务级 context（内存中）
//  2. Raw Episodic        —— 对话记录（conversations 表）
//  3. Consolidated Episodic —— 任务 summary（memories 表，tier=consolidated）  <-- 本文件
//  4. Semantic/Policy     —— 稳定规则（memories 表，tier=semantic）
//
// # Episode Summarization
//
// Phase 6-F：episode summarization 委托给 LLMSummarizer（LLM 调用失败时回退到旧的关键词
// 实现）。生成的 summary 包含：
//   - 使用的 tool 及其结果
//   - 遇到的错误
//   - 任务最终结果
//   - 关键观察
//
// # 自适应间隔
//
// heartbeat 间隔会根据活动量自适应：完成的任务越多，间隔越短（最小 30 秒）；任务越少，
// 间隔越长（最大 10 分钟）。这样可避免空闲时过度轮询，同时繁忙时保持 memory 新鲜。
package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// Heartbeat 周期性扫描已完成任务、生成 episode summary 并写入 consolidated memory 记录。
//
// 生命周期：
//
//	hb := NewHeartbeat(db.DB)
//	go hb.Start(context.Background())
//	defer hb.Stop()
type Heartbeat struct {
	db         MemoryDB
	summarizer LLMSummarizer
	interval   time.Duration
	state      *HeartbeatState
	mu         sync.Mutex
	cancel     context.CancelFunc
}

// HeartbeatState 跟踪 heartbeat 的进度，以便重启后能从上次 checkpoint 恢复。它以
// "heartbeat_state" 类型、tier "consolidated" 的 memory 记录持久化。
type HeartbeatState struct {
	// LastProcessedEventID 是最后处理的事件 ID。当前未使用 —— 为 Phase 6+ 的事件驱动
	// heartbeat 预留。
	LastProcessedEventID string `json:"last_processed_event_id"`

	// LastRunAt 是上次成功 heartbeat 周期的时间戳。
	LastRunAt time.Time `json:"last_run_at"`

	// ProcessedTaskCount 是自 heartbeat 启动以来累计处理的任务数。
	ProcessedTaskCount int `json:"processed_task_count"`

	// NextIntervalSeconds 是下一次 heartbeat 周期的间隔，由 adaptiveInterval 调整。
	NextIntervalSeconds int `json:"next_interval_seconds"`
}

// HeartbeatReport 汇总单次 heartbeat 周期的结果。
type HeartbeatReport struct {
	// NewTasksFound 是自上次 beat 以来发现已完成任务数。
	NewTasksFound int `json:"new_tasks_found"`

	// SummariesGenerated 是成功生成的 episode summary 数。
	SummariesGenerated int `json:"summaries_generated"`

	// MemoriesWritten 是写入 DB 的 memory 记录数。
	MemoriesWritten int `json:"memories_written"`

	// Errors 是本次 beat 遇到的错误数。
	Errors int `json:"errors"`

	// Duration 是本次 heartbeat 周期耗时。
	Duration time.Duration `json:"duration_ms"`

	// NextInterval 是下一次 heartbeat 周期的间隔。
	NextInterval time.Duration `json:"next_interval_ms"`
}

// MemoryDB 是 Heartbeat 与 PromotionGate 所需 DB 操作的最小接口。避免与 db package
// 直接耦合，便于测试。
type MemoryDB interface {
	QueryCompletedTaskIDs(since time.Time) ([]string, error)
	QueryConversationsByTask(taskID string) ([]db.ConversationRecord, error)
	QueryStepsByTaskForMemory(taskID string) ([]db.StepRecord, error)
	InsertMemory(record db.MemoryRecord) error
	QueryMemoriesByTier(projectID, tier string) ([]db.MemoryRecord, error)
	UpdateMemoryTier(id, tier, promotionReason string) error
}

// SqliteMemoryDB 将 db.DB（package 级 *sql.DB）适配到 MemoryDB 接口。它是 Heartbeat 与
// PromotionGate 使用的默认实现。
type SqliteMemoryDB struct{}

// QueryCompletedTaskIDs 委托给 db.QueryCompletedTaskIDs。
func (s *SqliteMemoryDB) QueryCompletedTaskIDs(since time.Time) ([]string, error) {
	return db.QueryCompletedTaskIDs(since)
}

// QueryConversationsByTask 委托给 db.QueryConversationsByTask。
func (s *SqliteMemoryDB) QueryConversationsByTask(taskID string) ([]db.ConversationRecord, error) {
	return db.QueryConversationsByTask(taskID)
}

// QueryStepsByTaskForMemory 委托给 db.QueryStepsByTaskForMemory。
func (s *SqliteMemoryDB) QueryStepsByTaskForMemory(taskID string) ([]db.StepRecord, error) {
	return db.QueryStepsByTaskForMemory(taskID)
}

// InsertMemory 委托给 db.InsertMemory。
func (s *SqliteMemoryDB) InsertMemory(record db.MemoryRecord) error {
	return db.InsertMemory(record)
}

// QueryMemoriesByTier 委托给 db.QueryMemoriesByTier。
func (s *SqliteMemoryDB) QueryMemoriesByTier(projectID, tier string) ([]db.MemoryRecord, error) {
	return db.QueryMemoriesByTier(projectID, tier)
}

// UpdateMemoryTier 委托给 db.UpdateMemoryTier。
func (s *SqliteMemoryDB) UpdateMemoryTier(id, tier, promotionReason string) error {
	return db.UpdateMemoryTier(id, tier, promotionReason)
}

// QuerySessionMessages 委托给 db.QuerySessionMessages。
func (s *SqliteMemoryDB) QuerySessionMessages(sessionID string) ([]db.SessionMessageRecord, error) {
	return db.QuerySessionMessages(sessionID)
}

// QuerySessionByID 委托给 db.QuerySessionByID。
func (s *SqliteMemoryDB) QuerySessionByID(sessionID string) (*db.SessionRecord, error) {
	return db.QuerySessionByID(sessionID)
}

// DeleteSessionMessagesBeforeTurn 委托给 db.DeleteSessionMessagesBeforeTurn。
func (s *SqliteMemoryDB) DeleteSessionMessagesBeforeTurn(sessionID string, turnIndex int) error {
	return db.DeleteSessionMessagesBeforeTurn(sessionID, turnIndex)
}

// UpdateSessionContextSize 委托给 db.UpdateSessionContextSize。
func (s *SqliteMemoryDB) UpdateSessionContextSize(sessionID string, totalTokens int, contextSize int) error {
	return db.UpdateSessionContextSize(sessionID, totalTokens, contextSize)
}

// NewHeartbeat 创建使用默认 5 分钟间隔的 Heartbeat。database 参数实现所有 DB 操作所需的
// MemoryDB 接口。summarizer 用于每个任务的 episode summary；若为 nil，Heartbeat 会回退到
// 自身的关键词实现以保留 Phase 5-B 行为。
func NewHeartbeat(database MemoryDB, summarizer LLMSummarizer) *Heartbeat {
	interval := 5 * time.Minute
	return &Heartbeat{
		db:         database,
		summarizer: summarizer,
		interval:   interval,
		state: &HeartbeatState{
			LastRunAt:           time.Now(),
			NextIntervalSeconds: int(interval.Seconds()),
		},
	}
}

// Start 在后台 goroutine 中启动 heartbeat loop。它创建可取消的 context 并在每个 tick
// 上运行 Beat()。调用 Stop() 可优雅停止 heartbeat。
func (hb *Heartbeat) Start(ctx context.Context) {
	ctx, hb.cancel = context.WithCancel(ctx)
	log.Printf("[Heartbeat] Started with interval %v", hb.interval)

	// 启动时立即运行一次，然后按 tick 运行
	go func() {
		// 初始 beat
		report, err := hb.Beat(ctx)
		if err != nil {
			log.Printf("[Heartbeat] Initial beat failed: %v", err)
		} else {
			log.Printf("[Heartbeat] Initial beat: %d tasks, %d memories, took %v",
				report.NewTasksFound, report.MemoriesWritten, report.Duration)
		}

		ticker := time.NewTicker(hb.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("[Heartbeat] Stopped")
				return
			case <-ticker.C:
				// 根据上次 report 的 NextInterval 调整 ticker 间隔
				if report != nil && report.NextInterval != hb.interval {
					ticker.Reset(report.NextInterval)
					hb.interval = report.NextInterval
				}

				report, err = hb.Beat(ctx)
				if err != nil {
					log.Printf("[Heartbeat] Beat failed: %v", err)
					continue
				}
				log.Printf("[Heartbeat] Beat: %d tasks, %d memories, took %v",
					report.NewTasksFound, report.MemoriesWritten, report.Duration)
			}
		}
	}()
}

// Stop 优雅停止 heartbeat loop。
func (hb *Heartbeat) Stop() {
	if hb.cancel != nil {
		hb.cancel()
	}
}

// Beat 执行单次 heartbeat 周期：
//  1. 扫描自上次 checkpoint 以来新完成的任务
//  2. 为每个新任务从对话生成 episode summary
//  3. 将候选 memory 写入 memories 表（tier=consolidated）
//  4. 更新 heartbeat 状态 checkpoint
//
// 返回 HeartbeatReport 汇总本次周期的结果。
func (hb *Heartbeat) Beat(ctx context.Context) (*HeartbeatReport, error) {
	start := time.Now()
	report := &HeartbeatReport{}

	hb.mu.Lock()
	checkpoint := hb.state.LastRunAt
	hb.mu.Unlock()

	// 1. 扫描自上次 checkpoint 以来新完成的任务
	taskIDs, err := hb.db.QueryCompletedTaskIDs(checkpoint)
	if err != nil {
		return report, fmt.Errorf("scan completed tasks: %w", err)
	}
	report.NewTasksFound = len(taskIDs)

	if len(taskIDs) == 0 {
		// 无新任务 —— 更新状态并返回
		hb.mu.Lock()
		hb.state.LastRunAt = time.Now()
		hb.state.NextIntervalSeconds = int(hb.adaptiveInterval(0).Seconds())
		hb.mu.Unlock()
		report.NextInterval = hb.adaptiveInterval(0)
		report.Duration = time.Since(start)
		return report, nil
	}

	// 2. 为每个新任务生成 episode summary 并写入 memory
	for _, taskID := range taskIDs {
		// 在任务之间检查 context 取消
		select {
		case <-ctx.Done():
			report.Duration = time.Since(start)
			return report, ctx.Err()
		default:
		}

		// 加载该任务的对话历史与 steps
		convs, convErr := hb.db.QueryConversationsByTask(taskID)
		steps, stepErr := hb.db.QueryStepsByTaskForMemory(taskID)
		if convErr != nil {
			log.Printf("[Heartbeat] Warning: failed to query conversations for task %s: %v", taskID, convErr)
		}
		if stepErr != nil {
			log.Printf("[Heartbeat] Warning: failed to query steps for task %s: %v", taskID, stepErr)
		}
		// Phase 6-F：优先使用 LLMSummarizer（内部会回退到关键词）；若未配置 summarizer，
		// 则直接走旧的关键词路径。
		var summary string
		var err error
		if hb.summarizer != nil {
			summary, err = hb.summarizer.SummarizeEpisode(ctx, taskID, convs, steps)
		}
		if summary == "" || err != nil {
			summary, err = hb.generateEpisodeSummary(ctx, taskID)
		}
		if err != nil {
			log.Printf("[Heartbeat] Failed to summarize task %s: %v", taskID, err)
			report.Errors++
			continue
		}
		report.SummariesGenerated++

		// 将 summary 作为 consolidated memory 记录写入
		memoryID := "mem_" + generateHexID(8)
		now := time.Now()
		memRecord := db.MemoryRecord{
			ID:             memoryID,
			ProjectID:      "default",
			Type:           "lesson", // episode summary 是一条学到的 lesson
			Tier:           "consolidated",
			Content:        summary,
			Confidence:     0.7, // 自动生成的 summary 置信度中等
			Status:         "active",
			SourceTaskIDs:  []string{taskID},
			SourceEventIDs: []string{},
			AccessCount:    0,
			CreatedAt:      now,
			UpdatedAt:      now,
		}

		if err := hb.db.InsertMemory(memRecord); err != nil {
			log.Printf("[Heartbeat] Failed to write memory for task %s: %v", taskID, err)
			report.Errors++
			continue
		}
		report.MemoriesWritten++
	}

	// 3. 更新 heartbeat 状态 checkpoint
	newInterval := hb.adaptiveInterval(report.NewTasksFound)
	hb.mu.Lock()
	hb.state.LastRunAt = time.Now()
	hb.state.ProcessedTaskCount += report.MemoriesWritten
	hb.state.NextIntervalSeconds = int(newInterval.Seconds())
	hb.mu.Unlock()

	report.NextInterval = newInterval
	report.Duration = time.Since(start)
	return report, nil
}

// adaptiveInterval 根据活动量调整 heartbeat 间隔。新任务越多 = 间隔越短（最小 30s）。
// 任务越少 = 间隔越长（最大 10min）。零任务 = 使用默认间隔。
func (hb *Heartbeat) adaptiveInterval(newEventCount int) time.Duration {
	switch {
	case newEventCount >= 10:
		return 30 * time.Second
	case newEventCount >= 5:
		return 1 * time.Minute
	case newEventCount >= 3:
		return 2 * time.Minute
	case newEventCount >= 1:
		return 5 * time.Minute
	default:
		return 10 * time.Minute
	}
}

// generateEpisodeSummary 读取某任务的对话历史并生成结构化 summary 字符串。这是保留为
// Phase 6-F LLMSummarizer 回退目标的旧关键词路径。KeywordSummarizer 接口的公开入口是
// 下面的 SummarizeEpisode。
//
// summary 捕获：
//   - 用户输入 / 任务 goal
//   - 调用的 tool 及其结果
//   - 遇到的错误
//   - 最终结果
//   - 关键观察
func (hb *Heartbeat) generateEpisodeSummary(ctx context.Context, taskID string) (string, error) {
	convs, err := hb.db.QueryConversationsByTask(taskID)
	if err != nil {
		return "", fmt.Errorf("query conversations for task %s: %w", taskID, err)
	}

	steps, err := hb.db.QueryStepsByTaskForMemory(taskID)
	if err != nil {
		// steps 是可选的 —— 记录日志并继续
		log.Printf("[Heartbeat] Warning: failed to query steps for task %s: %v", taskID, err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task: %s\n", taskID))

	// 从第一条 user message 中提取用户输入
	for _, c := range convs {
		if c.Role == "user" {
			sb.WriteString(fmt.Sprintf("Input: %s\n", truncateContent(c.Content, 200)))
			break
		}
	}

	// 从 steps 中提取 tool 调用及其结果
	toolCount := 0
	errorCount := 0
	for _, s := range steps {
		if s.Type == "tool_call" {
			toolCount++
			if s.ToolName != "" {
				sb.WriteString(fmt.Sprintf("Tool[%s]: %s", s.ToolName, truncateContent(s.ToolOutput, 100)))
				if s.Status == "failed" {
					sb.WriteString(" (FAILED)")
					errorCount++
				}
				sb.WriteString("\n")
			}
		}
	}

	// 从最后一条 assistant message 中提取最终结果
	var finalResult string
	for i := len(convs) - 1; i >= 0; i-- {
		if convs[i].Role == "assistant" {
			finalResult = convs[i].Content
			break
		}
	}
	sb.WriteString(fmt.Sprintf("Result: %s\n", truncateContent(finalResult, 300)))

	// 从对话中提取关键观察
	observations := extractObservations(convs)
	if len(observations) > 0 {
		sb.WriteString("Observations:\n")
		for _, obs := range observations {
			sb.WriteString(fmt.Sprintf("  - %s\n", truncateContent(obs, 150)))
		}
	}

	// 统计汇总
	sb.WriteString(fmt.Sprintf("Stats: %d tools, %d errors, %d messages\n",
		toolCount, errorCount, len(convs)))

	_ = ctx
	return sb.String(), nil
}

// extractObservations 扫描对话内容，找出形似观察的行（包含 tool 结果、错误信息或关键发现）。
// 这是 Phase 6 的简单关键词提取；基于 LLM 的提取会更准确。
func extractObservations(convs []db.ConversationRecord) []string {
	var observations []string
	// 暗示观察行的关键词
	obsKeywords := []string{
		"found", "result", "error", "failed", "success", "created",
		"modified", "deleted", "returned", "output", "generated",
		"completed", "analyzed", "detected", "discovered",
	}

	for _, c := range convs {
		if c.Role == "tool" || c.Role == "assistant" {
			lines := strings.Split(c.Content, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if len(line) < 10 || len(line) > 500 {
					continue
				}
				lower := strings.ToLower(line)
				for _, kw := range obsKeywords {
					if strings.Contains(lower, kw) {
						observations = append(observations, line)
						break
					}
				}
			}
		}
	}
	return observations
}

// truncateContent 将字符串截断到 maxLen 以便在 summary 中展示。
func truncateContent(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// generateHexID 生成给定字节数的随机十六进制字符串。用于 memory ID 与其他标识符。
func generateHexID(byteLen int) string {
	bytes := make([]byte, byteLen)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}