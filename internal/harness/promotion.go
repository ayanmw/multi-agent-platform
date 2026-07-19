// Package harness —— PromotionGate：将 consolidated memory 提升到 semantic tier。
//
// PromotionGate 评估 consolidated tier 中的候选 memory，将满足提升条件的提升到 semantic
// tier。它实现了 Consolidated Episodic 与 Semantic/Policy memory 之间的桥梁。
//
// # 三条提升通道
//
// 每条候选 memory 会针对三条提升通道进行检查：
//
//  1. repeated_across_sessions —— 该 memory 的 source_task_ids 至少包含 2 个不同的
//     task ID，意味着同一经验在独立任务中被观察到。这是最可靠模式的最强信号。
//
//  2. tool_failure_evidence —— 该 memory 的 source_event_ids 引用了一个 tool 调用
//     失败（status=failed）的事件。显式记录的失败是有价值的 lesson，应提升以便未来规避。
//
//  3. explicit_user_instruction —— 该 memory 的内容包含持久指令标记（如 "always"、
//     "以后都"、"rule:"、"preference:"）。这些是用户显式指令，应作为 semantic rule 持久化。
//
// # 提升生命周期
//
//   - Consolidated（候选） → Semantic（已提升）通过 PromotionGate.PromoteCandidates
//   - 提升是幂等的：已提升的 memory 会被跳过
//   - 提升原因记录在 memory 记录中以便审计
//   - gate 返回一份报告，包含已提升和已跳过 memory 的计数
package harness

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// PromotionGate 评估 consolidated 候选 memory 并将符合条件者提升到 semantic tier。
// 通常周期性调用（例如在 heartbeat 运行后）或通过 /api/memories/promote 端点按需调用。
type PromotionGate struct {
	db MemoryDB
}

// NewPromotionGate 用给定 MemoryDB 创建 PromotionGate。
func NewPromotionGate(database MemoryDB) *PromotionGate {
	return &PromotionGate{db: database}
}

// PromotionReport 汇总一次提升周期的结果。
type PromotionReport struct {
	// TotalCandidates 是被评估的 consolidated memory 数。
	TotalCandidates int `json:"total_candidates"`

	// PromotedCount 是被提升到 semantic tier 的 memory 数。
	PromotedCount int `json:"promoted_count"`

	// SkippedCount 是未满足任何提升条件的 memory 数。
	SkippedCount int `json:"skipped_count"`

	// Details 列出每条被提升 memory 及其提升原因。
	Details []PromotionDetail `json:"details"`

	// Duration 是本次提升周期耗时。
	Duration time.Duration `json:"duration_ms"`
}

// PromotionDetail 记录单条 memory 的提升，包含触发提升的通道。
type PromotionDetail struct {
	MemoryID        string `json:"memory_id"`
	Type            string `json:"type"`
	ContentSnippet  string `json:"content_snippet"`
	PromotionReason string `json:"promotion_reason"`
	// Channel 是触发提升的具体提升通道："repeated_across_sessions"、
	// "tool_failure_evidence" 或 "explicit_user_instruction"。
	Channel string `json:"channel"`
}

// PromoteCandidates 评估给定 project 的所有 consolidated memory，将符合条件者提升到
// semantic tier。
//
// 每条候选针对三条提升通道检查；只要任一通道通过，memory 即被提升。提升原因被记录，
// memory 的 tier 更新为 "semantic"。
//
// 已提升（tier=semantic）的 memory 会被跳过。
func (pg *PromotionGate) PromoteCandidates(projectID string) (*PromotionReport, error) {
	start := time.Now()
	report := &PromotionReport{}

	// 获取 project 的所有 consolidated memory
	candidates, err := pg.db.QueryMemoriesByTier(projectID, "consolidated")
	if err != nil {
		return report, fmt.Errorf("query consolidated memories: %w", err)
	}
	report.TotalCandidates = len(candidates)

	if len(candidates) == 0 {
		report.Duration = time.Since(start)
		return report, nil
	}

	for _, mem := range candidates {
		// 检查每条提升通道
		reason, channel := pg.evaluateCandidate(mem)
		if reason == "" {
			report.SkippedCount++
			continue
		}

		// 提升到 semantic tier
		if err := pg.db.UpdateMemoryTier(mem.ID, "semantic", reason); err != nil {
			log.Printf("[PromotionGate] Failed to promote memory %s: %v", mem.ID, err)
			report.SkippedCount++
			continue
		}

		report.PromotedCount++
		report.Details = append(report.Details, PromotionDetail{
			MemoryID:        mem.ID,
			Type:            mem.Type,
			ContentSnippet:  truncateContent(mem.Content, 100),
			PromotionReason: reason,
			Channel:         channel,
		})

		log.Printf("[PromotionGate] Promoted %s to semantic: %s (channel=%s)", mem.ID, reason, channel)
	}

	report.Duration = time.Since(start)
	return report, nil
}

// evaluateCandidate 针对三条提升通道检查单条 memory。若任一通道通过，返回提升原因与通道；
// 否则返回空字符串，表示该 memory 不应被提升。
func (pg *PromotionGate) evaluateCandidate(mem db.MemoryRecord) (reason string, channel string) {
	// 通道 1：repeated_across_sessions
	// 统计不同的 source_task_ids；若 >= 2，说明该经验在多个独立任务中被观察到 —— 它是
	// 可靠的模式。
	if len(mem.SourceTaskIDs) >= 2 {
		// 去重 task ID（以防数组中有重复）
		unique := make(map[string]bool)
		for _, tid := range mem.SourceTaskIDs {
			unique[tid] = true
		}
		if len(unique) >= 2 {
			reason = fmt.Sprintf("repeated_across_sessions: observed in %d distinct tasks", len(unique))
			channel = "repeated_across_sessions"
			return
		}
	}

	// 通道 2：tool_failure_evidence
	// 检查是否有 source_event_id 引用了失败的 tool 调用。这是基于 memory 内容与 metadata
	// 的轻量检查；完整的基于事件的校验需要查询 events 表（Phase 6+）。
	if pg.hasFailureEvidence(mem) {
		reason = "tool_failure_evidence: associated tool call failed"
		channel = "tool_failure_evidence"
		return
	}

	// 通道 3：explicit_user_instruction
	// 检查内容是否包含持久指令标记。
	if isExplicitInstruction(mem.Content) {
		reason = "explicit_user_instruction: content contains durable instruction marker"
		channel = "explicit_user_instruction"
		return
	}

	return "", ""
}

// hasFailureEvidence 检查 memory 的内容或来源数据是否表明出现了 tool 失败。这是基于
// 内容分析的轻量检查；Phase 6+ 会增加与 steps 表的交叉引用以做确切的失败检测。
func (pg *PromotionGate) hasFailureEvidence(mem db.MemoryRecord) bool {
	// 检查内容中的失败指示
	lower := strings.ToLower(mem.Content)
	failureMarkers := []string{
		"failed", "error", "exception", "timeout", "rejected",
		"blocked", "denied", "crashed", "aborted", "permission denied",
		"(FAILED)", "tool_call_failed",
	}
	for _, marker := range failureMarkers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// isExplicitInstruction 检查内容是否包含表明用户显式陈述了持久指令、偏好或规则的标记。
//
// 标记（中英文）：
//   - "always", "always do", "never", "never do"
//   - "rule:", "preference:", "policy:", "convention:"
//   - "以后都", "永远", "总是", "每次", "从不"
//   - "记住", "别忘了", "规定"
func isExplicitInstruction(content string) bool {
	lower := strings.ToLower(content)

	// 英文标记
	enMarkers := []string{
		"always", "never", "every time", "each time",
		"rule:", "preference:", "policy:", "convention:",
		"remember:", "don't forget", "do not forget",
		"from now on", "going forward", "henceforth",
	}
	for _, marker := range enMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	// 中文标记
	zhMarkers := []string{
		"以后都", "永远", "总是", "每次", "从不",
		"记住", "别忘了", "规定", "规则", "偏好",
		"以后", "以后每个", "从此", "今后",
	}
	for _, marker := range zhMarkers {
		if strings.Contains(content, marker) {
			return true
		}
	}

	return false
}