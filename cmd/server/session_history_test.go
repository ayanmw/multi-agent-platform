package main

import (
	"strings"
	"testing"

	"github.com/ayanmw/multi-agent-platform/pkg/db"
)

// N0-02 回归：多轮历史自复制。
//
// 缺陷链路：handleSessionChat 把历史文本前置进 system prompt → Engine 把
// 这份「带历史的」prompt 写回 session_messages → 下一轮再把它当历史读出来
// → 历史里嵌历史，随轮次递归膨胀。修复分两侧：
//   - 写侧：Engine 持久化 BaseSystemPrompt（干净基线），见 internal/runtime。
//   - 读侧：buildHistoryContext 跳过历史轮次的 system 行（本文件）。

const historyHeader = "## Previous Conversation History"

// TestBuildHistoryContext_SkipsPersistedSystemPrompts 验证读侧防线：
// 历史轮次的 system prompt（TurnIndex >= 0）绝不回灌，即使 DB 里残留着
// 修复前写入的、已经带历史的污染行，也不会被二次注入。
func TestBuildHistoryContext_SkipsPersistedSystemPrompts(t *testing.T) {
	polluted := historyHeader + "\n\n### Turn 1\n\n[user]: 旧问题\n\n## Current Task\n\nYou are a helpful assistant."
	msgs := []db.SessionMessageRecord{
		{TurnIndex: 0, Role: "system", Content: "You are a helpful assistant."},
		{TurnIndex: 0, Role: "user", Content: "我叫小明"},
		{TurnIndex: 0, Role: "assistant", Content: "你好，小明"},
		{TurnIndex: 1, Role: "system", Content: polluted}, // 修复前遗留的污染行
		{TurnIndex: 1, Role: "user", Content: "我叫什么"},
	}

	got := buildHistoryContext(msgs)

	if strings.Contains(got, "You are a helpful assistant.") {
		t.Fatalf("历史回灌中混入了 system prompt 基线:\n%s", got)
	}
	if strings.Count(got, historyHeader) != 1 {
		t.Fatalf("历史块标题出现 %d 次，期望恰好 1 次（>1 说明发生自复制）:\n%s",
			strings.Count(got, historyHeader), got)
	}
	for _, want := range []string{"我叫小明", "你好，小明", "我叫什么"} {
		if !strings.Contains(got, want) {
			t.Fatalf("历史回灌丢失了对话内容 %q:\n%s", want, got)
		}
	}
}

// TestBuildHistoryContext_KeepsCompressedSummary 验证唯一例外：
// ContextCompressor 写入的压缩摘要同样是 role="system"，但以 TurnIndex == -1
// 标记。它是被压缩掉的旧上下文的唯一载体，必须保留。
func TestBuildHistoryContext_KeepsCompressedSummary(t *testing.T) {
	msgs := []db.SessionMessageRecord{
		{TurnIndex: -1, Role: "system", Content: "## Compressed Summary\n用户自称小明。"},
		{TurnIndex: 2, Role: "system", Content: "You are a helpful assistant."},
		{TurnIndex: 2, Role: "user", Content: "继续"},
	}

	got := buildHistoryContext(msgs)

	if !strings.Contains(got, "用户自称小明。") {
		t.Fatalf("压缩摘要被误删:\n%s", got)
	}
	if strings.Contains(got, "You are a helpful assistant.") {
		t.Fatalf("历史轮次的 system prompt 未被过滤:\n%s", got)
	}
	// 摘要的 TurnIndex == -1，不应被冠上误导性的 "### Turn 0" 标题。
	if strings.Contains(got, "### Turn 0") {
		t.Fatalf("压缩摘要被错误地标为 Turn 0:\n%s", got)
	}
}

// TestBuildHistoryContext_EmptyWhenOnlySystemRows 验证过滤后无内容时返回空串，
// 让调用方跳过整段前置，避免向 prompt 注入只有标题、没有内容的空壳。
func TestBuildHistoryContext_EmptyWhenOnlySystemRows(t *testing.T) {
	msgs := []db.SessionMessageRecord{
		{TurnIndex: 0, Role: "system", Content: "You are a helpful assistant."},
		{TurnIndex: 1, Role: "system", Content: "You are a helpful assistant."},
	}
	if got := buildHistoryContext(msgs); got != "" {
		t.Fatalf("仅有 system 行时应返回空串，实际:\n%q", got)
	}
	if got := buildHistoryContext(nil); got != "" {
		t.Fatalf("空输入应返回空串，实际:\n%q", got)
	}
}

// TestBuildHistoryContext_NoGrowthAcrossTurns 端到端模拟多轮：每轮按修复后的
// 规则持久化（system 存干净基线）并回灌历史，断言历史块标题始终只出现一次，
// 即上下文不再随轮次递归套娃。
func TestBuildHistoryContext_NoGrowthAcrossTurns(t *testing.T) {
	const baseline = "You are a helpful assistant."
	var stored []db.SessionMessageRecord

	for turn := 0; turn < 5; turn++ {
		history := buildHistoryContext(stored)

		// 运行时 prompt = 历史 + 基线；持久化 prompt = 基线（修复后的写侧行为）。
		runtimePrompt := baseline
		if history != "" {
			runtimePrompt = history + "\n\n" + baseline
		}
		if strings.Count(runtimePrompt, historyHeader) > 1 {
			t.Fatalf("turn %d 运行时 prompt 中历史块出现 %d 次（自复制）",
				turn, strings.Count(runtimePrompt, historyHeader))
		}

		stored = append(stored,
			db.SessionMessageRecord{TurnIndex: turn, Role: "system", Content: baseline},
			db.SessionMessageRecord{TurnIndex: turn, Role: "user", Content: "问题"},
			db.SessionMessageRecord{TurnIndex: turn, Role: "assistant", Content: "回答"},
		)
	}

	final := buildHistoryContext(stored)
	if strings.Contains(final, baseline) {
		t.Fatalf("多轮后历史中仍混入 system prompt 基线:\n%s", final)
	}
	if strings.Count(final, historyHeader) != 1 {
		t.Fatalf("多轮后历史块标题出现 %d 次，期望 1 次", strings.Count(final, historyHeader))
	}
}
