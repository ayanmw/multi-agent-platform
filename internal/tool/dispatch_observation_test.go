package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// dispatch_sub_agent observation 标准化 (Phase 7-H2 阶段 5)
// ---------------------------------------------------------------------------

// fakeDispatcher 是 SubAgentDispatcher 的测试实现，按预设的 SubAgentResult 列表
// 原样返回，便于断言 dispatch_sub_agent 工具产出的 observation 结构。
type fakeDispatcher struct {
	results []SubAgentResult
	err     error
	gotID   string
	gotStrat string
}

func (f *fakeDispatcher) Dispatch(ctx context.Context, leaderSubTaskID, strategy string, agents []SubAgentSpec) ([]SubAgentResult, error) {
	f.gotID = leaderSubTaskID
	f.gotStrat = strategy
	return f.results, f.err
}

// runDispatch 调用 NewDispatchSubAgentTool 的 executor 并断言无 error。
func runDispatch(t *testing.T, tool *BuiltinTool, input map[string]any) map[string]any {
	t.Helper()
	got, err := tool.Execute(input)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("Execute returned %T, want map[string]any", got)
	}
	return m
}

// TestDispatchObservationAllCompleted 验证全部 worker 成功时顶层摘要字段。
func TestDispatchObservationAllCompleted(t *testing.T) {
	disp := &fakeDispatcher{results: []SubAgentResult{
		{AgentID: "a1", Name: "a1", Status: "completed", Result: "done1", TotalTokens: 100, Duration: 50},
		{AgentID: "a2", Name: "a2", Status: "completed", Result: "done2", TotalTokens: 200, Duration: 70},
	}}
	tool := NewDispatchSubAgentTool(disp, "root-task-123")
	out := runDispatch(t, tool, map[string]any{
		"reason":   "split work",
		"strategy": "parallel",
		"agents": []any{
			map[string]any{"agent_id": "a1", "system_prompt": "p1"},
			map[string]any{"agent_id": "a2", "system_prompt": "p2"},
		},
	})

	if got := out["dispatched"]; got != true {
		t.Errorf("dispatched = %v, want true", got)
	}
	if got := out["all_completed"]; got != true {
		t.Errorf("all_completed = %v, want true", got)
	}
	if got := out["completed_count"]; got != 2 {
		t.Errorf("completed_count = %v, want 2", got)
	}
	if got := out["total_tokens"]; got != 300 {
		t.Errorf("total_tokens = %v, want 300", got)
	}
	if got := out["agent_count"]; got != 2 {
		t.Errorf("agent_count = %v, want 2", got)
	}
	summary, _ := out["summary"].(string)
	if !strings.Contains(summary, "all completed") {
		t.Errorf("summary = %q, want contains 'all completed'", summary)
	}
	if disp.gotID != "root-task-123" {
		t.Errorf("dispatcher received leaderSubTaskID = %q, want root-task-123", disp.gotID)
	}
	if disp.gotStrat != "parallel" {
		t.Errorf("dispatcher received strategy = %q, want parallel", disp.gotStrat)
	}

	results, _ := out["results"].([]map[string]any)
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	if results[0]["succeeded"] != true {
		t.Errorf("results[0].succeeded = %v, want true", results[0]["succeeded"])
	}
	if results[0]["result_truncated"] != false {
		t.Errorf("results[0].result_truncated = %v, want false", results[0]["result_truncated"])
	}
}

// TestDispatchObservationPartialFailure 验证部分 worker 失败/skipped 时
// all_completed=false 与 completed_count 的正确性，确认 leader 不会被
// "skipped" 误导为成功。
func TestDispatchObservationPartialFailure(t *testing.T) {
	disp := &fakeDispatcher{results: []SubAgentResult{
		{AgentID: "a1", Status: "completed", Result: "ok", TotalTokens: 10},
		{AgentID: "a2", Status: "failed", Error: "boom", TotalTokens: 5},
		{AgentID: "a3", Status: "skipped", Result: "upstream skipped"},
	}}
	tool := NewDispatchSubAgentTool(disp, "root")
	out := runDispatch(t, tool, map[string]any{
		"reason":   "test",
		"strategy": "sequential",
		"agents": []any{
			map[string]any{"agent_id": "a1", "system_prompt": "p"},
			map[string]any{"agent_id": "a2", "system_prompt": "p"},
			map[string]any{"agent_id": "a3", "system_prompt": "p"},
		},
	})

	if got := out["all_completed"]; got != false {
		t.Errorf("all_completed = %v, want false when any non-completed", got)
	}
	if got := out["completed_count"]; got != 1 {
		t.Errorf("completed_count = %v, want 1", got)
	}
	summary, _ := out["summary"].(string)
	if !strings.Contains(summary, "1 completed") || !strings.Contains(summary, "2 failed/skipped") {
		t.Errorf("summary = %q, want counts of completed and failed/skipped", summary)
	}

	results, _ := out["results"].([]map[string]any)
	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3", len(results))
	}
	byID := map[string]map[string]any{}
	for _, r := range results {
		byID[r["agent_id"].(string)] = r
	}
	if byID["a1"]["succeeded"] != true {
		t.Errorf("a1.succeeded = %v, want true", byID["a1"]["succeeded"])
	}
	if byID["a2"]["succeeded"] != false {
		t.Errorf("a2.succeeded = %v, want false", byID["a2"]["succeeded"])
	}
	if byID["a3"]["succeeded"] != false {
		t.Errorf("a3.succeeded = %v, want false (skipped must not be succeeded)", byID["a3"]["succeeded"])
	}
	if byID["a2"]["error"] != "boom" {
		t.Errorf("a2.error = %v, want boom", byID["a2"]["error"])
	}
}

// TestDispatchObservationResultTruncation 验证超长 result 被截断且设置
// result_truncated 标记，确保 leader 上下文不会被单个 worker 巨量输出撑爆。
func TestDispatchObservationResultTruncation(t *testing.T) {
	long := strings.Repeat("x", 5000)
	disp := &fakeDispatcher{results: []SubAgentResult{
		{AgentID: "a1", Status: "completed", Result: long, TotalTokens: 1},
	}}
	tool := NewDispatchSubAgentTool(disp, "root")
	out := runDispatch(t, tool, map[string]any{
		"reason":   "t",
		"strategy": "parallel",
		"agents":   []any{map[string]any{"agent_id": "a1", "system_prompt": "p"}},
	})
	results, _ := out["results"].([]map[string]any)
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r0 := results[0]
	if r0["result_truncated"] != true {
		t.Errorf("result_truncated = %v, want true", r0["result_truncated"])
	}
	got, _ := r0["result"].(string)
	if len(got) >= 5000 {
		t.Errorf("truncated result len = %d, want < 5000", len(got))
	}
	if !strings.HasSuffix(got, "...[truncated]") {
		t.Errorf("truncated result suffix = %q, want ...[truncated]", got[len(got)-15:])
	}
}

// TestDispatchObservationError 验证 dispatcher 返回 error 时工具向上抛错，
// leader 的 ReAct loop 会把它作为失败 observation 处理。
func TestDispatchObservationError(t *testing.T) {
	disp := &fakeDispatcher{err: errors.New("orchestrator down")}
	tool := NewDispatchSubAgentTool(disp, "root")
	_, err := tool.Execute(map[string]any{
		"reason":   "t",
		"strategy": "parallel",
		"agents":   []any{map[string]any{"agent_id": "a1", "system_prompt": "p"}},
	})
	if err == nil || !strings.Contains(err.Error(), "orchestrator down") {
		t.Fatalf("Execute error = %v, want contains 'orchestrator down'", err)
	}
}

// TestTruncateObservationBoundary 覆盖 truncateObservation 的边界行为。
func TestTruncateObservationBoundary(t *testing.T) {
	if got := truncateObservation("short", 100); got != "short" {
		t.Errorf("short string should pass through, got %q", got)
	}
	if got := truncateObservation("abc", 0); got != "abc" {
		t.Errorf("maxBytes<=0 should disable truncation, got %q", got)
	}
	if got := truncateObservation("abcdef", 3); got != "abc...[truncated]" {
		t.Errorf("ascii truncation = %q, want abc...[truncated]", got)
	}
	// 中文字符占 3 字节，确保不会在多字节中间截断产生乱码。
	s := strings.Repeat("中", 10) // 30 bytes
	got := truncateObservation(s, 8) // 期望回退到 6 字节边界 (2 个 "中")
	if !strings.HasSuffix(got, "...[truncated]") {
		t.Errorf("utf8 truncation suffix wrong: %q", got)
	}
	// 截断后的前缀部分必须是合法 UTF-8（此处即整数个 "中"）。
	prefix := strings.TrimSuffix(got, "...[truncated]")
	for _, r := range prefix {
		if r != '中' {
			t.Errorf("utf8 truncation produced non-中 rune %q in prefix %q", r, prefix)
		}
	}
}
