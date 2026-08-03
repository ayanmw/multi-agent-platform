package main

// session_history.go —— 多轮会话历史回读（N1-01）。
//
// 职责：把 session_messages 表里的行还原成 LLM 可直接消费的原生
// []llm.Message，交给 runtime.Engine 插在 system prompt 之后、本轮 user
// input 之前。
//
// 为什么单独成文件：历史回读是「读侧」的完整子系统（过滤 → 裁剪 → 还原
// → 配对清洗 → 截断），与 api.go 里的 HTTP handler 关注点不同；独立文件
// 便于针对性测试（session_history_test.go）与后续演进（例如按 token 而非
// 字符裁剪）。
//
// 演进脉络：
//   - N0-02 修掉了「持久化 prompt 自复制」这个 bug，但历史仍被 buildHistoryContext
//     压扁成一段 markdown 文本前置进 system prompt（接错层）。
//   - N1-01（本文件）把历史下沉为原生消息数组：role 边界、tool_calls 与
//     tool_call_id 的配对关系被完整保留，system prompt 恢复为稳定前缀。

import (
	"encoding/json"
	"fmt"

	"github.com/ayanmw/multi-agent-platform/internal/llm"
	"github.com/ayanmw/multi-agent-platform/pkg/db"
)

// historyLimits 是一次历史回读的边界，取自 config.SessionHistoryLimits。
// 用独立类型而非直接依赖 config，是为了让 buildHistoryMessages 在测试中
// 可以脱离 .env / 全局配置构造。
type historyLimits struct {
	// MaxTurns 是回读的最近对话轮数上限；<=0 表示不限。
	MaxTurns int
	// MaxMessageChars 是单条消息的字符上限；<=0 表示不截断。
	MaxMessageChars int
}

// buildHistoryMessages 把 session_messages 记录还原为原生 []llm.Message。
//
// 输入 msgs 必须按 (turn_index ASC, created_at ASC) 排序——即
// db.QuerySessionMessages 的返回顺序；本函数依赖该顺序做 tool_call 配对。
//
// 处理流水线（每一步都对应一个必须成立的不变量）：
//
//  1. **过滤历史轮次的 system 基线**（Role=="system" && TurnIndex>=0）。
//     它是那一轮 agent 收到的指令，不是对话内容；回灌它等于把旧指令伪装
//     成用户说过的话，且会与「system prompt 前置」机制叠加成递归套娃
//     （N0-02 的根因）。
//     唯一例外是 ContextCompressor 写入的压缩摘要：同样 role="system"，
//     但用 TurnIndex == -1 标记（见 internal/harness/compressor.go），它是
//     被压缩掉的旧上下文的唯一载体，必须保留。
//
//  2. **按轮数裁剪**：只保留最近 limits.MaxTurns 个 turn_index。压缩摘要
//     （TurnIndex == -1）不占配额，恒保留并排在最前——它描述的正是被裁掉
//     的那部分历史。
//
//  3. **还原消息**：assistant 行的 tool_calls JSON 反序列化回 []llm.ToolCall；
//     tool 行带回 tool_call_id。
//
//  4. **tool_call 配对清洗**：OpenAI 兼容接口要求「带 tool_calls 的 assistant
//     消息后必须紧跟每个 call 对应的 tool 消息」，否则整个请求 400。而
//     session_messages 里天然可能出现悬空的一侧——审批被拒、任务被取消、
//     进程崩溃、或本轮历史刚好被轮数上限从中间截断。因此这里剔除：
//     没有对应 tool 结果的 tool_call 条目，以及找不到发起方的 tool 消息。
//
//  5. **截断**：单条超过 limits.MaxMessageChars 的内容截断并标注，避免一次
//     超长 tool 输出（例如整个文件）独占上下文窗口。
//
// 返回 nil 表示「无可回读的历史」，调用方据此完全跳过历史注入。
func buildHistoryMessages(msgs []db.SessionMessageRecord, limits historyLimits) []llm.Message {
	kept := filterHistoryRecords(msgs, limits.MaxTurns)
	if len(kept) == 0 {
		return nil
	}

	out := make([]llm.Message, 0, len(kept))
	for _, m := range kept {
		msg := llm.Message{
			Role:    m.Role,
			Content: truncateHistoryContent(m.Content, limits.MaxMessageChars),
		}
		switch m.Role {
		case "assistant":
			// tool_calls 为空串是常态（纯文本回复）；解析失败视为无 tool call，
			// 不阻断整段历史——宁可少一次 tool 上下文，也不要丢掉整轮对话。
			if m.ToolCalls != "" {
				var calls []llm.ToolCall
				if err := json.Unmarshal([]byte(m.ToolCalls), &calls); err != nil {
					log.Warnf("server", "[SessionHistory] 无法解析 tool_calls（已忽略该字段）: %v", err)
				} else {
					msg.ToolCalls = calls
				}
			}
		case "tool":
			msg.ToolCallID = m.ToolCallID
		}
		out = append(out, msg)
	}

	out = sanitizeToolCallPairs(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// filterHistoryRecords 执行流水线的第 1、2 步：过滤历史 system 基线，并按
// 轮数上限只保留最近若干轮。压缩摘要（TurnIndex == -1）不占轮数配额。
//
// maxTurns <= 0 表示不限轮数。
func filterHistoryRecords(msgs []db.SessionMessageRecord, maxTurns int) []db.SessionMessageRecord {
	kept := make([]db.SessionMessageRecord, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" && m.TurnIndex >= 0 {
			continue // 历史轮次的指令基线：跳过，防止 N0-02 自复制
		}
		kept = append(kept, m)
	}
	if maxTurns <= 0 || len(kept) == 0 {
		return kept
	}

	// 收集出现过的真实轮次（TurnIndex >= 0），确定保留窗口的下界。
	// 用「最大轮次 - maxTurns + 1」而不是「去重计数」，因为轮次可能不连续
	// （某轮全是被过滤掉的 system 行），按数值切窗语义更稳定。
	maxTurn := -1
	for _, m := range kept {
		if m.TurnIndex > maxTurn {
			maxTurn = m.TurnIndex
		}
	}
	if maxTurn < 0 {
		return kept // 只有压缩摘要，无需裁剪
	}
	minTurn := maxTurn - maxTurns + 1
	if minTurn <= 0 {
		return kept // 总轮数未超上限
	}

	windowed := make([]db.SessionMessageRecord, 0, len(kept))
	for _, m := range kept {
		if m.TurnIndex < 0 || m.TurnIndex >= minTurn {
			windowed = append(windowed, m)
		}
	}
	return windowed
}

// sanitizeToolCallPairs 执行流水线的第 4 步：保证 assistant.tool_calls 与
// 后续 tool 消息严格一一配对。
//
// 为什么必须做：OpenAI 兼容接口对这段结构的校验是硬性的——
//   - assistant 带了 tool_calls 却没有对应的 tool 消息 → 400
//   - tool 消息的 tool_call_id 找不到发起方 → 400
//
// 而 session_messages 里出现悬空的一侧是完全正常的：审批被拒绝、用户中途
// 取消、进程崩溃、或历史刚好被轮数窗口从一次 tool 调用中间切开。历史回读
// 绝不能因为这些正常情况让整个下一轮请求失败。
//
// 清洗规则：
//   - 剔除无结果的 tool_call 条目；若某条 assistant 的 tool_calls 因此清空
//     且 Content 也为空，则整条丢弃（空 assistant 消息无意义且部分 provider 拒收）。
//   - 剔除找不到发起方的 tool 消息。
func sanitizeToolCallPairs(msgs []llm.Message) []llm.Message {
	// 第一遍：收集所有已有结果的 tool_call_id。
	answered := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}

	out := make([]llm.Message, 0, len(msgs))
	// issued 记录已被保留下来的 tool_call ID，供 tool 消息反查发起方。
	issued := make(map[string]bool)

	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			if len(m.ToolCalls) > 0 {
				calls := make([]llm.ToolCall, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					if tc.ID != "" && answered[tc.ID] {
						calls = append(calls, tc)
						issued[tc.ID] = true
					}
				}
				m.ToolCalls = calls
				if len(calls) == 0 && m.Content == "" {
					continue // 既无有效 tool call 又无正文：整条丢弃
				}
			}
			out = append(out, m)
		case "tool":
			if m.ToolCallID == "" || !issued[m.ToolCallID] {
				continue // 悬空的 tool 结果：丢弃
			}
			out = append(out, m)
		default:
			out = append(out, m)
		}
	}
	return out
}

// truncateHistoryContent 按**字符（rune）**而非字节截断历史消息内容。
//
// 为什么不复用 api.go 的 truncateContent：那个函数按字节切片，会把一个多
// 字节 UTF-8 字符从中间劈开，产生非法码点。它原先只用于生成给人看的历史
// 摘要文本，问题不明显；而这里的输出会直接作为 message 发给 LLM——非法
// UTF-8 会被部分 provider 判为 400，或让模型读到乱码。中文会话是本项目的
// 主要场景，必须按 rune 切。
//
// maxChars <= 0 表示不截断（配置显式关闭上限时的语义）。
func truncateHistoryContent(content string, maxChars int) string {
	if maxChars <= 0 {
		return content
	}
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	// 标注被截断，让模型知道这里存在信息缺失，而不是把残句当作完整内容。
	return string(runes[:maxChars]) + "\n...[truncated]"
}

// historyMessageCount 统计各角色的历史消息条数，供日志与可观测事件使用。
// 白盒哲学：历史注入是影响 LLM 行为的关键输入，必须可被观察到注入了多少、
// 都是什么角色，而不是悄无声息地改变上下文。
func historyMessageCount(msgs []llm.Message) string {
	var user, assistant, toolMsg, system int
	for _, m := range msgs {
		switch m.Role {
		case "user":
			user++
		case "assistant":
			assistant++
		case "tool":
			toolMsg++
		case "system":
			system++
		}
	}
	return fmt.Sprintf("total=%d user=%d assistant=%d tool=%d summary=%d",
		len(msgs), user, assistant, toolMsg, system)
}
