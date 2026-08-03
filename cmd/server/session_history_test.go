package main

import (
	"strings"
	"testing"

	"github.com/ayanmw/multi-agent-platform/internal/llm"
	"github.com/ayanmw/multi-agent-platform/pkg/db"
)

// 本文件覆盖读侧的两条防线：
//
//   - N0-02（多轮历史自复制）：历史轮次的 system 基线绝不回读，压缩摘要
//     （TurnIndex == -1）必须保留。
//   - N1-01（历史下沉为原生消息数组）：还原 role/tool_calls/tool_call_id，
//     悬空的 tool 配对必须清洗掉，轮数与单条长度受显式上限约束。
//
// N0-02 的断言在 N1-01 之后依然成立——只是断言对象从「拼出来的文本」变成
// 了「还原出的消息数组」，不变量本身没变。

// defaultTestLimits 与 config.LoadSessionHistoryLimits 的默认值保持一致，
// 避免测试与生产默认漂移。
var defaultTestLimits = historyLimits{MaxTurns: 20, MaxMessageChars: 4000}

// containsContent 判断消息数组里是否存在包含给定子串的消息。
func containsContent(msgs []llm.Message, want string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, want) {
			return true
		}
	}
	return false
}

// TestBuildHistoryMessages_SkipsPersistedSystemPrompts 验证 N0-02 读侧防线：
// 历史轮次的 system prompt（TurnIndex >= 0）绝不回读，即使 DB 里残留着
// 修复前写入的、已经带历史的污染行，也不会被二次注入。
func TestBuildHistoryMessages_SkipsPersistedSystemPrompts(t *testing.T) {
	polluted := "## Previous Conversation History\n\n### Turn 1\n\n[user]: 旧问题\n\n## Current Task\n\nYou are a helpful assistant."
	msgs := []db.SessionMessageRecord{
		{TurnIndex: 0, Role: "system", Content: "You are a helpful assistant."},
		{TurnIndex: 0, Role: "user", Content: "我叫小明"},
		{TurnIndex: 0, Role: "assistant", Content: "你好，小明"},
		{TurnIndex: 1, Role: "system", Content: polluted}, // 修复前遗留的污染行
		{TurnIndex: 1, Role: "user", Content: "我叫什么"},
	}

	got := buildHistoryMessages(msgs, defaultTestLimits)

	for _, m := range got {
		if m.Role == "system" {
			t.Fatalf("历史回读中混入了 system 基线: %q", m.Content)
		}
	}
	if len(got) != 3 {
		t.Fatalf("期望回读 3 条对话消息，实际 %d 条: %+v", len(got), got)
	}
	for _, want := range []string{"我叫小明", "你好，小明", "我叫什么"} {
		if !containsContent(got, want) {
			t.Fatalf("历史回读丢失了对话内容 %q: %+v", want, got)
		}
	}
	// 角色顺序必须与落库顺序一致，否则 LLM 无法还原对话时序。
	wantRoles := []string{"user", "assistant", "user"}
	for i, want := range wantRoles {
		if got[i].Role != want {
			t.Fatalf("第 %d 条角色 = %q，期望 %q", i, got[i].Role, want)
		}
	}
}

// TestBuildHistoryMessages_KeepsCompressedSummary 验证唯一例外：
// ContextCompressor 写入的压缩摘要同样是 role="system"，但以 TurnIndex == -1
// 标记。它是被压缩掉的旧上下文的唯一载体，必须保留。
func TestBuildHistoryMessages_KeepsCompressedSummary(t *testing.T) {
	msgs := []db.SessionMessageRecord{
		{TurnIndex: -1, Role: "system", Content: "## Compressed Summary\n用户自称小明。"},
		{TurnIndex: 2, Role: "system", Content: "You are a helpful assistant."},
		{TurnIndex: 2, Role: "user", Content: "继续"},
	}

	got := buildHistoryMessages(msgs, defaultTestLimits)

	if !containsContent(got, "用户自称小明。") {
		t.Fatalf("压缩摘要被误删: %+v", got)
	}
	if containsContent(got, "You are a helpful assistant.") {
		t.Fatalf("历史轮次的 system prompt 未被过滤: %+v", got)
	}
	if got[0].Role != "system" {
		t.Fatalf("压缩摘要应以 system 角色排在最前，实际首条为 %q", got[0].Role)
	}
}

// TestBuildHistoryMessages_NilWhenNoConversation 验证过滤后无内容时返回 nil，
// 让调用方完全跳过历史注入，而不是注入一段空壳。
func TestBuildHistoryMessages_NilWhenNoConversation(t *testing.T) {
	msgs := []db.SessionMessageRecord{
		{TurnIndex: 0, Role: "system", Content: "You are a helpful assistant."},
		{TurnIndex: 1, Role: "system", Content: "You are a helpful assistant."},
	}
	if got := buildHistoryMessages(msgs, defaultTestLimits); got != nil {
		t.Fatalf("仅有 system 基线时应返回 nil，实际: %+v", got)
	}
	if got := buildHistoryMessages(nil, defaultTestLimits); got != nil {
		t.Fatalf("空输入应返回 nil，实际: %+v", got)
	}
}

// TestBuildHistoryMessages_NoGrowthAcrossTurns 端到端模拟多轮：每轮按修复后的
// 规则持久化（system 存干净基线），断言回读出的历史条数严格等于「已发生的
// 对话消息数」，即不存在任何形式的自复制或重复放大。
func TestBuildHistoryMessages_NoGrowthAcrossTurns(t *testing.T) {
	const baseline = "You are a helpful assistant."
	var stored []db.SessionMessageRecord

	for turn := 0; turn < 5; turn++ {
		history := buildHistoryMessages(stored, defaultTestLimits)
		// 每轮开始前，历史应恰好是此前 turn 轮 × 2 条（user + assistant）。
		if want := turn * 2; len(history) != want {
			t.Fatalf("turn %d 回读到 %d 条历史，期望 %d 条（多出即自复制）",
				turn, len(history), want)
		}
		for _, m := range history {
			if strings.Contains(m.Content, baseline) {
				t.Fatalf("turn %d 历史中混入 system prompt 基线: %q", turn, m.Content)
			}
		}

		stored = append(stored,
			db.SessionMessageRecord{TurnIndex: turn, Role: "system", Content: baseline},
			db.SessionMessageRecord{TurnIndex: turn, Role: "user", Content: "问题"},
			db.SessionMessageRecord{TurnIndex: turn, Role: "assistant", Content: "回答"},
		)
	}
}

// TestBuildHistoryMessages_RestoresToolCallPairs 验证 N1-01 的核心收益：
// assistant 的 tool_calls 与 tool 消息的 tool_call_id 被完整还原，而不是
// 压扁成文本后丢失配对关系。
func TestBuildHistoryMessages_RestoresToolCallPairs(t *testing.T) {
	msgs := []db.SessionMessageRecord{
		{TurnIndex: 0, Role: "user", Content: "看一下当前目录"},
		{TurnIndex: 0, Role: "assistant", Content: "我来执行",
			ToolCalls: `[{"index":0,"id":"call_1","type":"function","function":{"name":"run_shell","arguments":"{\"command\":\"ls\"}"}}]`},
		{TurnIndex: 0, Role: "tool", Content: "a.go b.go", ToolCallID: "call_1"},
		{TurnIndex: 0, Role: "assistant", Content: "目录下有 a.go 和 b.go"},
	}

	got := buildHistoryMessages(msgs, defaultTestLimits)

	if len(got) != 4 {
		t.Fatalf("期望 4 条消息，实际 %d 条: %+v", len(got), got)
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("assistant 的 tool_calls 未还原: %+v", got[1])
	}
	if got[1].ToolCalls[0].ID != "call_1" || got[1].ToolCalls[0].Function.Name != "run_shell" {
		t.Fatalf("tool_call 内容还原错误: %+v", got[1].ToolCalls[0])
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "call_1" {
		t.Fatalf("tool 消息的 tool_call_id 未还原: %+v", got[2])
	}
}

// TestBuildHistoryMessages_DropsDanglingToolCalls 验证配对清洗：
// 没有结果的 tool_call 与找不到发起方的 tool 消息都必须被剔除，否则
// OpenAI 兼容接口会直接 400，让整个下一轮对话失败。
//
// 悬空是正常现象——审批被拒、任务被取消、进程崩溃，或历史刚好被轮数窗口
// 从一次 tool 调用中间切开。
func TestBuildHistoryMessages_DropsDanglingToolCalls(t *testing.T) {
	msgs := []db.SessionMessageRecord{
		// 情况 1：assistant 发起了 tool_call 但没有结果（例如被审批拒绝）。
		{TurnIndex: 0, Role: "user", Content: "删除文件"},
		{TurnIndex: 0, Role: "assistant", Content: "准备删除",
			ToolCalls: `[{"index":0,"id":"call_dangling","type":"function","function":{"name":"run_shell","arguments":"{}"}}]`},
		// 情况 2：tool 结果找不到发起方（历史被窗口切开的残留）。
		{TurnIndex: 1, Role: "tool", Content: "orphan result", ToolCallID: "call_missing"},
		{TurnIndex: 1, Role: "user", Content: "算了"},
	}

	got := buildHistoryMessages(msgs, defaultTestLimits)

	for _, m := range got {
		if len(m.ToolCalls) > 0 {
			t.Fatalf("无结果的 tool_call 未被剔除: %+v", m)
		}
		if m.Role == "tool" {
			t.Fatalf("悬空的 tool 消息未被剔除: %+v", m)
		}
	}
	// assistant 有正文，应保留正文本身（只是去掉了 tool_calls）。
	if !containsContent(got, "准备删除") {
		t.Fatalf("assistant 正文被误删: %+v", got)
	}
	if !containsContent(got, "算了") {
		t.Fatalf("用户消息被误删: %+v", got)
	}
}

// TestSanitizeToolCallPairs_DropsEmptyAssistant 验证边界：assistant 既无正文
// 又无有效 tool_call 时整条丢弃——空 assistant 消息无意义，且部分 provider 拒收。
func TestSanitizeToolCallPairs_DropsEmptyAssistant(t *testing.T) {
	in := []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call_x"}}}, // 无正文 + 无结果
	}
	got := sanitizeToolCallPairs(in)
	if len(got) != 1 || got[0].Role != "user" {
		t.Fatalf("空 assistant 未被丢弃: %+v", got)
	}
}

// TestBuildHistoryMessages_RespectsTurnWindow 验证轮数上限：只保留最近
// MaxTurns 轮，且压缩摘要（TurnIndex == -1）不占配额、恒保留。
func TestBuildHistoryMessages_RespectsTurnWindow(t *testing.T) {
	msgs := []db.SessionMessageRecord{
		{TurnIndex: -1, Role: "system", Content: "摘要：早期对话"},
	}
	for turn := 0; turn < 5; turn++ {
		msgs = append(msgs,
			db.SessionMessageRecord{TurnIndex: turn, Role: "user", Content: turnTag(turn)},
			db.SessionMessageRecord{TurnIndex: turn, Role: "assistant", Content: "ok"},
		)
	}

	got := buildHistoryMessages(msgs, historyLimits{MaxTurns: 2, MaxMessageChars: 4000})

	// 期望：摘要 + turn3(2条) + turn4(2条) = 5 条。
	if len(got) != 5 {
		t.Fatalf("轮数窗口=2 时期望 5 条（摘要+最近2轮），实际 %d 条: %+v", len(got), got)
	}
	if !containsContent(got, "摘要：早期对话") {
		t.Fatalf("压缩摘要不应占用轮数配额: %+v", got)
	}
	for _, dropped := range []int{0, 1, 2} {
		if containsContent(got, turnTag(dropped)) {
			t.Fatalf("turn %d 应已被窗口裁掉: %+v", dropped, got)
		}
	}
	for _, kept := range []int{3, 4} {
		if !containsContent(got, turnTag(kept)) {
			t.Fatalf("turn %d 应被保留: %+v", kept, got)
		}
	}
}

// turnTag 生成可被子串匹配唯一识别的轮次标记。
func turnTag(turn int) string {
	return "TURN-" + string(rune('A'+turn)) + "-MARK"
}

// TestTruncateHistoryContent_RuneSafe 验证按 rune 截断：多字节字符不得被
// 从中间劈开（会产生非法 UTF-8，被部分 provider 判为 400）。
func TestTruncateHistoryContent_RuneSafe(t *testing.T) {
	const content = "中文内容测试" // 6 个 rune，18 字节
	got := truncateHistoryContent(content, 3)
	if !strings.HasPrefix(got, "中文内") {
		t.Fatalf("按 rune 截断失败，实际: %q", got)
	}
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("截断后应标注省略，实际: %q", got)
	}
	if !utf8Valid(got) {
		t.Fatalf("截断产生了非法 UTF-8: %q", got)
	}
	// 未超上限时原样返回；上限 <=0 表示不截断。
	if got := truncateHistoryContent(content, 100); got != content {
		t.Fatalf("未超上限时应原样返回，实际: %q", got)
	}
	if got := truncateHistoryContent(content, 0); got != content {
		t.Fatalf("上限 <=0 应不截断，实际: %q", got)
	}
}

// utf8Valid 是 utf8.ValidString 的本地别名，避免测试文件为一处校验单独引包。
func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}
