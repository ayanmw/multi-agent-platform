package harness

// summarizer_test.go — LLMSummarizerImpl / HubEmitter / KeywordAdapter /
// BuildKeywordEpisodeSummary 的单元测试。
//
// 覆盖：
//   - SummarizeTurn / SummarizeEpisode 正常路径（LLM 返回固定 content）
//   - 失败时回退到 KeywordSummarizer，并发出 completed(fallback_used=true)
//   - 超时触发回退
//   - nil keyword adapter 路径返回 error
//   - HubEmitter nil hub 不 panic / 正常 forward
//   - BuildKeywordEpisodeSummary 使用 fake MemoryDB 产出含 task_id / 工具统计 / Result 的结构化文本
//
// 测试不依赖真实 LLM 网络与 SQLite，使用 MockProvider + InMemoryMockScriptStore
// 作为 Provider，recorderEmitter 收集事件，fakeMemoryDB 实现 MemoryDB 接口。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// ============================================================================
// 辅助：事件收集器（实现 EventEmitter）
// ============================================================================

// recorderEmitter 记录 LLMSummarizerImpl 发出的所有事件，供测试断言顺序与内容。
type recorderEmitter struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	Type string
	Data map[string]any
}

func (r *recorderEmitter) Emit(eventType string, data map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{Type: eventType, Data: data})
}

func (r *recorderEmitter) snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

func (r *recorderEmitter) byType(t string) []recordedEvent {
	all := r.snapshot()
	out := make([]recordedEvent, 0, len(all))
	for _, e := range all {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// ============================================================================
// 辅助：假 HubSender（实现 HubEmitter.Hub）
// ============================================================================

// fakeHub 记录所有 SendEvent 调用，提供给 HubEmitter 验证 forward 行为。
type fakeHub struct {
	mu      sync.Mutex
	events  []interface{}
	failErr error
}

func (h *fakeHub) SendEvent(evt interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failErr != nil {
		return
	}
	h.events = append(h.events, evt)
}

func (h *fakeHub) snapshot() []interface{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]interface{}, len(h.events))
	copy(out, h.events)
	return out
}

// ============================================================================
// 辅助：假 MemoryDB
// ============================================================================

// fakeMemoryDB 满足 MemoryDB 接口（包含 CompressorDB 所需方法），
// 返回预置的 conversations / steps 用于 BuildKeywordEpisodeSummary 测试。
type fakeMemoryDB struct {
	convs  map[string][]db.ConversationRecord
	steps  map[string][]db.StepRecord
	tasks  []string
	memErr error
}

func newFakeMemoryDB() *fakeMemoryDB {
	return &fakeMemoryDB{
		convs: make(map[string][]db.ConversationRecord),
		steps: make(map[string][]db.StepRecord),
	}
}

func (f *fakeMemoryDB) QueryCompletedTaskIDs(_ time.Time) ([]string, error) {
	if f.memErr != nil {
		return nil, f.memErr
	}
	return f.tasks, nil
}

func (f *fakeMemoryDB) QueryConversationsByTask(taskID string) ([]db.ConversationRecord, error) {
	if f.memErr != nil {
		return nil, f.memErr
	}
	return f.convs[taskID], nil
}

func (f *fakeMemoryDB) QueryStepsByTaskForMemory(taskID string) ([]db.StepRecord, error) {
	if f.memErr != nil {
		return nil, f.memErr
	}
	return f.steps[taskID], nil
}

func (f *fakeMemoryDB) InsertMemory(_ db.MemoryRecord) error { return nil }

func (f *fakeMemoryDB) QueryMemoriesByTier(_, _ string) ([]db.MemoryRecord, error) {
	return nil, nil
}

func (f *fakeMemoryDB) UpdateMemoryTier(_, _, _ string) error { return nil }

func (f *fakeMemoryDB) QuerySessionMessages(_ string) ([]db.SessionMessageRecord, error) {
	return nil, nil
}

func (f *fakeMemoryDB) QuerySessionByID(_ string) (*db.SessionRecord, error) {
	return nil, nil
}

func (f *fakeMemoryDB) DeleteSessionMessagesBeforeTurn(_ string, _ int) error {
	return nil
}

func (f *fakeMemoryDB) UpdateSessionContextSize(_ string, _, _ int) error {
	return nil
}

// ============================================================================
// 辅助：构造带脚本的 MockProvider
// ============================================================================

// newMockProviderWithScript 用 caseID + 固定 content 构造一个最小的 mock provider，
// 用于触发 keyword fallback 之外的正常 LLM 路径。
func newMockProviderWithScript(t *testing.T, caseID, content string, delayMs int) *llm.MockProvider {
	t.Helper()
	store := llm.NewInMemoryMockScriptStore()
	script := llm.MockScript{
		ID:     "test:" + caseID,
		CaseID: caseID,
		Priority: 1000,
		Responses: []llm.MockResponse{
			{Type: llm.MockResponseText, Content: content, DelayMs: delayMs},
		},
	}
	if _, err := store.Save(script); err != nil {
		t.Fatalf("save mock script: %v", err)
	}
	return llm.NewMockProvider("mock", store, nil)
}

// newEmptyMockProvider 故意不带任何脚本，Chat 必返回 "no matching mock script"。
func newEmptyMockProvider() *llm.MockProvider {
	store := llm.NewInMemoryMockScriptStore()
	return llm.NewMockProvider("mock", store, nil)
}

// fixedKeywordSummarizer 是测试用的 fallback 实现：返回固定字符串，
// 并通过 counter 统计调用次数，确认 LLM 失败时确实走了 fallback 路径。
type fixedKeywordSummarizer struct {
	mu              sync.Mutex
	turnCalls       int
	episodeCalls    int
	turnOut         string
	episodeOut      string
	turnErr         error
	episodeErr      error
}

func (k *fixedKeywordSummarizer) SummarizeTurn(_ context.Context, _ int, _ []db.SessionMessageRecord) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.turnCalls++
	if k.turnErr != nil {
		return "", k.turnErr
	}
	return k.turnOut, nil
}

func (k *fixedKeywordSummarizer) SummarizeEpisode(_ context.Context, _ string, _ []db.ConversationRecord, _ []db.StepRecord) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.episodeCalls++
	if k.episodeErr != nil {
		return "", k.episodeErr
	}
	return k.episodeOut, nil
}

// ============================================================================
// SummarizeTurn — 正常路径
// ============================================================================

func TestLLMSummarizerImpl_SummarizeTurn_OK(t *testing.T) {
	provider := newMockProviderWithScript(t, "summarizer-turn", "turn summary from LLM", 0)
	rec := &recorderEmitter{}
	kw := &fixedKeywordSummarizer{turnOut: "kw-fallback"}

	impl := NewLLMSummarizerImpl(provider, "test-model", kw, rec)
	msgs := []db.SessionMessageRecord{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	out, err := impl.SummarizeTurn(context.Background(), 0, msgs)
	if err != nil {
		t.Fatalf("SummarizeTurn err: %v", err)
	}
	if out != "turn summary from LLM" {
		t.Errorf("out=%q, want %q", out, "turn summary from LLM")
	}

	// emitter 收到 started + completed(fallback_used=false)
	started := rec.byType(EventMemorySummarizeStarted)
	completed := rec.byType(EventMemorySummarizeCompleted)
	if len(started) != 1 {
		t.Errorf("expected 1 started event, got %d", len(started))
	}
	if len(completed) != 1 {
		t.Errorf("expected 1 completed event, got %d", len(completed))
	}
	if v, ok := completed[0].Data["fallback_used"]; !ok || v != false {
		t.Errorf("completed.fallback_used should be false, got %v", v)
	}
	// LLM 成功时不走 keyword
	if kw.turnCalls != 0 {
		t.Errorf("keyword fallback should not be called, got %d calls", kw.turnCalls)
	}
}

// ============================================================================
// SummarizeTurn — fallback 路径
// ============================================================================

func TestLLMSummarizerImpl_SummarizeTurn_Fallback(t *testing.T) {
	provider := newEmptyMockProvider() // 无脚本 → Chat 必失败
	rec := &recorderEmitter{}
	kw := &fixedKeywordSummarizer{turnOut: "kw-keyword-fallback"}

	impl := NewLLMSummarizerImpl(provider, "test-model", kw, rec)
	out, err := impl.SummarizeTurn(context.Background(), 0, []db.SessionMessageRecord{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatalf("expected nil err when fallback succeeds, got %v", err)
	}
	if out != "kw-keyword-fallback" {
		t.Errorf("out=%q, want keyword fallback output", out)
	}
	if kw.turnCalls != 1 {
		t.Errorf("keyword fallback should be called once, got %d", kw.turnCalls)
	}

	completed := rec.byType(EventMemorySummarizeCompleted)
	if len(completed) != 1 {
		t.Fatalf("expected 1 completed event, got %d", len(completed))
	}
	if v, ok := completed[0].Data["fallback_used"]; !ok || v != true {
		t.Errorf("completed.fallback_used should be true, got %v", v)
	}
	if _, ok := completed[0].Data["llm_error"]; !ok {
		t.Errorf("completed.llm_error should be present, got %v", completed[0].Data)
	}
}

// ============================================================================
// SummarizeTurn — 超时触发 fallback
// ============================================================================

func TestLLMSummarizerImpl_SummarizeTurn_Timeout(t *testing.T) {
	// 脚本自带 200ms 延迟；超时设置为 1ms 必触发 ctx.Done() → fallback
	provider := newMockProviderWithScript(t, "summarizer-timeout", "late", 200)
	rec := &recorderEmitter{}
	kw := &fixedKeywordSummarizer{turnOut: "kw-on-timeout"}

	impl := NewLLMSummarizerImpl(provider, "test-model", kw, rec)
	impl.timeout = 1 * time.Millisecond

	out, err := impl.SummarizeTurn(context.Background(), 0, []db.SessionMessageRecord{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatalf("expected nil err when fallback succeeds after timeout, got %v", err)
	}
	if out != "kw-on-timeout" {
		t.Errorf("out=%q, want kw-on-timeout", out)
	}
	if kw.turnCalls != 1 {
		t.Errorf("keyword fallback should be called once after timeout, got %d", kw.turnCalls)
	}
	completed := rec.byType(EventMemorySummarizeCompleted)
	if len(completed) != 1 {
		t.Fatalf("expected 1 completed event, got %d", len(completed))
	}
	if v, _ := completed[0].Data["fallback_used"]; v != true {
		t.Errorf("fallback_used should be true after timeout, got %v", v)
	}
}

// ============================================================================
// SummarizeEpisode — 正常路径
// ============================================================================

func TestLLMSummarizerImpl_SummarizeEpisode_OK(t *testing.T) {
	provider := newMockProviderWithScript(t, "summarizer-episode", "episode summary from LLM", 0)
	rec := &recorderEmitter{}
	kw := &fixedKeywordSummarizer{episodeOut: "kw-episode"}

	impl := NewLLMSummarizerImpl(provider, "test-model", kw, rec)
	out, err := impl.SummarizeEpisode(context.Background(), "task-1",
		[]db.ConversationRecord{{Role: "user", Content: "hi"}},
		[]db.StepRecord{{Type: "tool_call", ToolName: "run_shell", Status: "ok"}})
	if err != nil {
		t.Fatalf("SummarizeEpisode err: %v", err)
	}
	if out != "episode summary from LLM" {
		t.Errorf("out=%q, want %q", out, "episode summary from LLM")
	}

	started := rec.byType(EventMemorySummarizeStarted)
	completed := rec.byType(EventMemorySummarizeCompleted)
	if len(started) != 1 {
		t.Errorf("expected 1 started event, got %d", len(started))
	}
	if len(completed) != 1 {
		t.Errorf("expected 1 completed event, got %d", len(completed))
	}
	if v, _ := completed[0].Data["fallback_used"]; v != false {
		t.Errorf("fallback_used should be false, got %v", v)
	}
	if v, _ := completed[0].Data["task_id"]; v != "task-1" {
		t.Errorf("task_id should be task-1, got %v", v)
	}
	if kw.episodeCalls != 0 {
		t.Errorf("keyword fallback should not be called, got %d", kw.episodeCalls)
	}
}

// ============================================================================
// SummarizeEpisode — fallback 路径
// ============================================================================

func TestLLMSummarizerImpl_SummarizeEpisode_Fallback(t *testing.T) {
	provider := newEmptyMockProvider()
	rec := &recorderEmitter{}
	kw := &fixedKeywordSummarizer{episodeOut: "kw-episode-fallback"}

	impl := NewLLMSummarizerImpl(provider, "test-model", kw, rec)
	out, err := impl.SummarizeEpisode(context.Background(), "task-fb",
		[]db.ConversationRecord{{Role: "user", Content: "x"}}, nil)
	if err != nil {
		t.Fatalf("expected nil err on fallback success, got %v", err)
	}
	if out != "kw-episode-fallback" {
		t.Errorf("out=%q", out)
	}
	if kw.episodeCalls != 1 {
		t.Errorf("keyword fallback expected once, got %d", kw.episodeCalls)
	}
	completed := rec.byType(EventMemorySummarizeCompleted)
	if v, _ := completed[0].Data["fallback_used"]; v != true {
		t.Errorf("fallback_used should be true, got %v", v)
	}
	if v, _ := completed[0].Data["task_id"]; v != "task-fb" {
		t.Errorf("task_id should be task-fb, got %v", v)
	}
}

// ============================================================================
// KeywordAdapter — nil fn 路径返回 error
// ============================================================================

func TestKeywordAdapter_TurnEpisode(t *testing.T) {
	t.Run("nil turnFn returns error", func(t *testing.T) {
		// 只接 episode，turn 走 nil
		adapter := NewKeywordAdapter(nil, func(_ context.Context, _ string, _ []db.ConversationRecord, _ []db.StepRecord) (string, error) {
			return "ep", nil
		})
		_, err := adapter.SummarizeTurn(context.Background(), 0, nil)
		if err == nil {
			t.Fatal("expected error for nil turnFn")
		}
		if !strings.Contains(err.Error(), "turn") {
			t.Errorf("err should mention 'turn', got %v", err)
		}
	})

	t.Run("nil episodeFn returns error", func(t *testing.T) {
		adapter := NewKeywordAdapter(func(_ context.Context, _ int, _ []db.SessionMessageRecord) (string, error) {
			return "turn", nil
		}, nil)
		_, err := adapter.SummarizeEpisode(context.Background(), "task", nil, nil)
		if err == nil {
			t.Fatal("expected error for nil episodeFn")
		}
		if !strings.Contains(err.Error(), "episode") {
			t.Errorf("err should mention 'episode', got %v", err)
		}
	})

	t.Run("both wired through", func(t *testing.T) {
		turnCalled := false
		epCalled := false
		adapter := NewKeywordAdapter(
			func(_ context.Context, turnIndex int, _ []db.SessionMessageRecord) (string, error) {
				turnCalled = true
				if turnIndex != 3 {
					t.Errorf("turnIndex = %d, want 3", turnIndex)
				}
				return "turn-out", nil
			},
			func(_ context.Context, taskID string, _ []db.ConversationRecord, _ []db.StepRecord) (string, error) {
				epCalled = true
				if taskID != "t-7" {
					t.Errorf("taskID = %q, want t-7", taskID)
				}
				return "ep-out", nil
			},
		)
		out, err := adapter.SummarizeTurn(context.Background(), 3, nil)
		if err != nil || out != "turn-out" || !turnCalled {
			t.Errorf("turn adapter failed: out=%q err=%v called=%v", out, err, turnCalled)
		}
		out, err = adapter.SummarizeEpisode(context.Background(), "t-7", nil, nil)
		if err != nil || out != "ep-out" || !epCalled {
			t.Errorf("episode adapter failed: out=%q err=%v called=%v", out, err, epCalled)
		}
	})
}

// ============================================================================
// BuildKeywordEpisodeSummary — fake MemoryDB → 结构化文本
// ============================================================================

func TestBuildKeywordEpisodeSummary(t *testing.T) {
	memDB := newFakeMemoryDB()
	memDB.convs["task-X"] = []db.ConversationRecord{
		{Role: "user", Content: "please fix bug"},
		{Role: "assistant", Content: "investigating"},
		{Role: "tool", Content: "tool output"},
		{Role: "assistant", Content: "bug fixed"},
	}
	memDB.steps["task-X"] = []db.StepRecord{
		{Type: "tool_call", ToolName: "run_shell", Status: "ok", ToolOutput: "ok output"},
		{Type: "tool_call", ToolName: "write_file", Status: "failed", ToolOutput: "fail output"},
		{Type: "observation", Content: "found the issue"},
	}

	out, err := BuildKeywordEpisodeSummary(memDB, "task-X")
	if err != nil {
		t.Fatalf("BuildKeywordEpisodeSummary err: %v", err)
	}

	// 必须含 task_id / 工具调用统计 / final result
	if !strings.Contains(out, "task-X") {
		t.Errorf("output missing task_id, got: %s", out)
	}
	if !strings.Contains(out, "please fix bug") {
		t.Errorf("output missing user input, got: %s", out)
	}
	if !strings.Contains(out, "Tool[run_shell]") {
		t.Errorf("output missing run_shell tool entry, got: %s", out)
	}
	if !strings.Contains(out, "Tool[write_file]") || !strings.Contains(out, "FAILED") {
		t.Errorf("output missing failed write_file entry, got: %s", out)
	}
	if !strings.Contains(out, "bug fixed") {
		t.Errorf("output missing final assistant result, got: %s", out)
	}
	if !strings.Contains(out, "Stats: 2 tools") {
		t.Errorf("output should report 2 tool calls, got: %s", out)
	}
	if !strings.Contains(out, "1 errors") {
		t.Errorf("output should report 1 error, got: %s", out)
	}
	if !strings.Contains(out, "4 messages") {
		t.Errorf("output should report 4 messages, got: %s", out)
	}
}

// ============================================================================
// HubEmitter — nil hub 不 panic / 正常 forward
// ============================================================================

func TestHubEmitter_NilHub_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Emit must not panic on nil hub, got panic=%v", r)
		}
	}()

	t.Run("nil receiver", func(t *testing.T) {
		var h *HubEmitter
		h.Emit("evt", map[string]any{"k": "v"})
	})
	t.Run("nil Hub field", func(t *testing.T) {
		h := &HubEmitter{Hub: nil}
		h.Emit("evt", map[string]any{"k": "v"})
	})
}

func TestHubEmitter_Forwards(t *testing.T) {
	hub := &fakeHub{}
	h := &HubEmitter{Hub: hub}

	h.Emit("memory_summarize_completed", map[string]any{"kind": "turn", "fallback_used": true})
	h.Emit("memory_summarize_failed", map[string]any{"kind": "episode", "error": "boom"})

	got := hub.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 forwarded events, got %d", len(got))
	}
	for i, evt := range got {
		m, ok := evt.(map[string]any)
		if !ok {
			t.Fatalf("event %d not a map: %T", i, evt)
		}
		if _, ok := m["type"]; !ok {
			t.Errorf("event %d missing 'type' field: %v", i, m)
		}
		if _, ok := m["data"]; !ok {
			t.Errorf("event %d missing 'data' field: %v", i, m)
		}
	}
}

// ============================================================================
// 补充：summarizer.go 文档中提到的 "provider 为 nil" 错误路径，
// 确保兜底行为可观测（caller 总会拿到 err 而不是 panic）。
// ============================================================================

func TestLLMSummarizerImpl_NilProvider_ReturnsError(t *testing.T) {
	impl := NewLLMSummarizerImpl(nil, "test-model", nil, nil) // 无 provider 无 fallback
	_, err := impl.SummarizeTurn(context.Background(), 0, nil)
	if err == nil {
		t.Fatal("expected error when provider and fallback are both nil")
	}
	if !strings.Contains(err.Error(), "no fallback") {
		t.Errorf("err should mention 'no fallback', got %v", err)
	}

	_, err = impl.SummarizeEpisode(context.Background(), "task", nil, nil)
	if err == nil {
		t.Fatal("expected error for episode with nil provider")
	}
}

// ============================================================================
// 补充：fallback 也失败时，summarizer 仍返回 error（caller 拿到非 nil err）。
// ============================================================================

func TestLLMSummarizerImpl_BothFail_ReturnsError(t *testing.T) {
	provider := newEmptyMockProvider()
	rec := &recorderEmitter{}
	kw := &fixedKeywordSummarizer{turnErr: errors.New("kw boom"), episodeErr: errors.New("ep boom")}

	impl := NewLLMSummarizerImpl(provider, "test-model", kw, rec)

	_, err := impl.SummarizeTurn(context.Background(), 0, nil)
	if err == nil {
		t.Fatal("expected error when both LLM and fallback fail")
	}
	if !strings.Contains(err.Error(), "kw boom") {
		t.Errorf("err should wrap kw boom, got %v", err)
	}
	failed := rec.byType(EventMemorySummarizeFailed)
	if len(failed) != 1 {
		t.Errorf("expected 1 failed event, got %d", len(failed))
	}

	_, err = impl.SummarizeEpisode(context.Background(), "task", nil, nil)
	if err == nil {
		t.Fatal("expected episode error when both fail")
	}
	if !strings.Contains(err.Error(), "ep boom") {
		t.Errorf("err should wrap ep boom, got %v", err)
	}
}
