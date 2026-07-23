package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestSelectScriptCaseIDMatch 验证 selectScript（通过 Chat 触发）会选中
// CaseID 等于 ChatRequest.CaseID 的脚本。此处不加载 builtin，
// 以隔离 case_id 匹配，不受 priority/keyword 评分影响。
func TestSelectScriptCaseIDMatch(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	scripts := []MockScript{
		{
			ID:         "case-dialogue",
			CaseID:     "dialogue",
			MatchInput: []string{"hello"},
			Responses: []MockResponse{
				{Type: MockResponseText, Content: "matched-by-case-id"},
			},
		},
	}
	if _, err := store.Save(scripts[0]); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := NewMockProvider("mock", store, nil)

	resp, err := p.Chat(ChatRequest{
		Model:   "test-model",
		CaseID:  "dialogue",
		Messages: []Message{{Role: "user", Content: "anything"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := resp.Choices[0].Message.Content; got != "matched-by-case-id" {
		t.Fatalf("expected matched-by-case-id, got %q", got)
	}
}

// TestSelectScriptKeywordFallback 验证当 CaseID 为空、但 user 消息
// 含某脚本的 MatchInput 关键字时，该脚本会被选中。
func TestSelectScriptKeywordFallback(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	scripts := []MockScript{
		{
			ID:         "kw-weather",
			CaseID:     "weather", // CaseID 存在，但 request.CaseID 为空
			MatchInput: []string{"weather"},
			Responses: []MockResponse{
				{Type: MockResponseText, Content: "sunny"},
			},
		},
	}
	if _, err := store.Save(scripts[0]); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := NewMockProvider("mock", store, nil)

	resp, err := p.Chat(ChatRequest{
		Messages: []Message{{Role: "user", Content: "what is the weather today"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := resp.Choices[0].Message.Content; got != "sunny" {
		t.Fatalf("expected sunny, got %q", got)
	}
}

// TestSelectScriptCaseIDBeatsSubstring 验证精确 CaseID 命中（+1000）会胜过
// 仅靠输入子串命中 CaseID（+500）的脚本。这防止常见英文词 case ID（如
// "research"）靠子串劫持其它 case 的 run-case 路径。
func TestSelectScriptCaseIDBeatsSubstring(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	// research 脚本：输入 "research" 子串命中 +500，加 Priority 100 = 600。
	research := MockScript{
		ID:         "builtin:research",
		CaseID:     "research",
		Priority:   100,
		MatchInput: []string{"research"},
		Responses:  []MockResponse{{Type: MockResponseText, Content: "research-script"}},
	}
	// multi-agent-sequential 脚本：CaseID 与 request.CaseID 精确命中 +1000，
	// 加 Priority 100 = 1100。其输入恰好也含 "research" 子串。
	seq := MockScript{
		ID:         "builtin:multi-agent-sequential",
		CaseID:     "multi-agent-sequential",
		Priority:   100,
		MatchInput: []string{"sequential"},
		Responses:  []MockResponse{{Type: MockResponseText, Content: "sequential-script"}},
	}
	if _, err := store.Save(research); err != nil {
		t.Fatalf("Save research: %v", err)
	}
	if _, err := store.Save(seq); err != nil {
		t.Fatalf("Save seq: %v", err)
	}
	p := NewMockProvider("mock", store, nil)

	// request.CaseID=multi-agent-sequential 且输入含 "research"：
	// 精确命中应让 sequential 脚本胜出，而非 research 脚本靠子串抢走。
	resp, err := p.Chat(ChatRequest{
		CaseID:   "multi-agent-sequential",
		Messages: []Message{{Role: "user", Content: "Research the pros and cons of serverless vs containers, then write a report"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := resp.Choices[0].Message.Content; got != "sequential-script" {
		t.Fatalf("expected sequential-script (exact CaseID match), got %q", got)
	}
}

// TestDynamicScriptOverridesBuiltin 验证：与某 builtin 同 CaseID 但
// Priority 更高的动态脚本会胜出（selectScript 中动态脚本先于 builtin 追加，
// 且 priority 计入评分）。
func TestDynamicScriptOverridesBuiltin(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	dyn := MockScript{
		ID:       "dyn:dialogue",
		CaseID:   "dialogue",
		Priority: 500, // 高于 builtin 的 100
		Responses: []MockResponse{
			{Type: MockResponseText, Content: "dynamic-wins"},
		},
	}
	if _, err := store.Save(dyn); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := NewMockProvider("mock", store, BuiltinMockScripts())

	resp, err := p.Chat(ChatRequest{
		CaseID:   "dialogue",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got := resp.Choices[0].Message.Content; got != "dynamic-wins" {
		t.Fatalf("expected dynamic-wins, got %q", got)
	}
}

// TestResponseSequenceProgression 验证对同一 case 连续两次 ChatStream
// 会依次返回 responses[0]、responses[1]，第三次调用会 clamp 到最后一条响应。
func TestResponseSequenceProgression(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	script := MockScript{
		ID:     "seq",
		CaseID: "seq-case",
		Responses: []MockResponse{
			{Type: MockResponseToolCall, ToolCalls: []ToolCall{{Idx: 0, ID: "c1", Type: "function", Function: FunctionCall{Name: "run_shell", Arguments: "{}"}}}},
			{Type: MockResponseText, Content: "final-text"},
		},
	}
	if _, err := store.Save(script); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := NewMockProvider("mock", store, nil)
	req := ChatRequest{
		CaseID:   "seq-case",
		Messages: []Message{{Role: "user", Content: "go"}},
	}

	// 第一次调用：tool_call 响应。
	content, _, toolCalls, err := p.ChatStream(req, nil)
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if content != "" || len(toolCalls) != 1 || toolCalls[0].ID != "c1" {
		t.Fatalf("call 1: expected tool_call c1, got content=%q toolCalls=%v", content, toolCalls)
	}

	// 第二次调用：text 响应。
	content, _, toolCalls, err = p.ChatStream(req, nil)
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if content != "final-text" || len(toolCalls) != 0 {
		t.Fatalf("call 2: expected final-text, got content=%q toolCalls=%v", content, toolCalls)
	}

	// 第三次调用：clamp 到最后一条响应（text）。
	content, _, toolCalls, err = p.ChatStream(req, nil)
	if err != nil {
		t.Fatalf("call 3: %v", err)
	}
	if content != "final-text" || len(toolCalls) != 0 {
		t.Fatalf("call 3: expected clamp to final-text, got content=%q toolCalls=%v", content, toolCalls)
	}
}

// TestUsageCalculation 验证 text 与 tool_call 响应的 Usage 字段。
func TestUsageCalculation(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	textContent := "abc"
	userInput := "hello" // len 5
	script := MockScript{
		ID:       "usage-text",
		CaseID:   "usage-text",
		Priority: 1000,
		Responses: []MockResponse{
			{Type: MockResponseText, Content: textContent},
		},
	}
	if _, err := store.Save(script); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 已知 ToolCalls 字符串表示的 tool-call 脚本。
	tc := []ToolCall{{Idx: 0, ID: "x", Type: "function", Function: FunctionCall{Name: "run_shell", Arguments: "{}"}}}
	toolCallScript := MockScript{
		ID:       "usage-tc",
		CaseID:   "usage-tc",
		Priority: 1000,
		Responses: []MockResponse{
			{Type: MockResponseToolCall, ToolCalls: tc},
		},
	}
	if _, err := store.Save(toolCallScript); err != nil {
		t.Fatalf("Save tc: %v", err)
	}

	p := NewMockProvider("mock", store, nil)

	// Text 响应：PromptTokens = len(userInput)，CompletionTokens = len(content)。
	_, usage, _, err := p.ChatStream(ChatRequest{
		CaseID:   "usage-text",
		Messages: []Message{{Role: "user", Content: userInput}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream text: %v", err)
	}
	if usage.PromptTokens != len(userInput) {
		t.Errorf("text PromptTokens: got %d want %d", usage.PromptTokens, len(userInput))
	}
	if usage.CompletionTokens != len(textContent) {
		t.Errorf("text CompletionTokens: got %d want %d", usage.CompletionTokens, len(textContent))
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Errorf("text TotalTokens: got %d want %d", usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
	}

	// Tool-call 响应：CompletionTokens = len(fmt.Sprintf("%v", ToolCalls))。
	_, usage, _, err = p.ChatStream(ChatRequest{
		CaseID:   "usage-tc",
		Messages: []Message{{Role: "user", Content: userInput}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream tc: %v", err)
	}
	wantCompletion := len(fmt.Sprintf("%v", tc))
	if usage.PromptTokens != len(userInput) {
		t.Errorf("tc PromptTokens: got %d want %d", usage.PromptTokens, len(userInput))
	}
	if usage.CompletionTokens != wantCompletion {
		t.Errorf("tc CompletionTokens: got %d want %d", usage.CompletionTokens, wantCompletion)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Errorf("tc TotalTokens: got %d want %d", usage.TotalTokens, usage.PromptTokens+usage.CompletionTokens)
	}
}

// TestNoMatchingScriptReturnsError 验证：store 为空、输入无关键字命中、
// CaseID 为空时，会返回包含 "no matching mock script" 的 error。
func TestNoMatchingScriptReturnsError(t *testing.T) {
	store := NewInMemoryMockScriptStore() // 空
	p := NewMockProvider("mock", store, nil)

	_, err := p.Chat(ChatRequest{
		Messages: []Message{{Role: "user", Content: "zzz"}},
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no matching mock script") {
		t.Fatalf("error should mention 'no matching mock script', got %q", err.Error())
	}
}

// TestDelayMsZeroFastPath 验证 DelayMs=0 时不会阻塞。
func TestDelayMsZeroFastPath(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	script := MockScript{
		ID:       "no-delay",
		CaseID:   "no-delay",
		Priority: 1000,
		Responses: []MockResponse{
			{Type: MockResponseText, Content: "fast", DelayMs: 0},
		},
	}
	if _, err := store.Save(script); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := NewMockProvider("mock", store, nil)

	start := time.Now()
	resp, err := p.Chat(ChatRequest{
		CaseID:   "no-delay",
		Messages: []Message{{Role: "user", Content: "go"}},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Choices[0].Message.Content != "fast" {
		t.Fatalf("got %q", resp.Choices[0].Message.Content)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("DelayMs=0 should not block, elapsed=%v", elapsed)
	}
}

// TestDelayMsHonorsContextCancel 验证正 DelayMs 会响应 context 取消。
func TestDelayMsHonorsContextCancel(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	script := MockScript{
		ID:       "slow",
		CaseID:   "slow",
		Priority: 1000,
		Responses: []MockResponse{
			{Type: MockResponseText, Content: "should-not-see", DelayMs: 500},
		},
	}
	if _, err := store.Save(script); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := NewMockProvider("mock", store, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, _, err := p.ChatStream(ChatRequest{
		CaseID:   "slow",
		Context:  ctx,
		Messages: []Message{{Role: "user", Content: "go"}},
	}, nil)
	if err == nil {
		t.Fatalf("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

// TestInMemoryMockScriptStoreCRUD 覆盖 Save/Get/List/Delete 与 LoadBuiltin。
func TestInMemoryMockScriptStoreCRUD(t *testing.T) {
	t.Run("Save_assigns_ID_and_timestamps", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		got, err := s.Save(MockScript{CaseID: "x"})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if got.ID == "" {
			t.Fatal("Save should assign ID when empty")
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
			t.Fatal("Save should set CreatedAt/UpdatedAt")
		}
	})

	t.Run("Save_preserves_existing_ID", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		got, err := s.Save(MockScript{ID: "fixed-id", CaseID: "y"})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
		if got.ID != "fixed-id" {
			t.Fatalf("expected fixed-id, got %q", got.ID)
		}
	})

	t.Run("Get_missing_returns_error", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		_, err := s.Get("nope")
		if err == nil {
			t.Fatal("expected error for missing ID")
		}
	})

	t.Run("Get_after_Save", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		got, _ := s.Save(MockScript{ID: "g1", CaseID: "c"})
		r, err := s.Get(got.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if r.CaseID != "c" {
			t.Fatalf("got CaseID=%q", r.CaseID)
		}
	})

	t.Run("List_empty_then_two", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		list, err := s.List()
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 0 {
			t.Fatalf("expected 0, got %d", len(list))
		}
		s.Save(MockScript{ID: "a"})
		s.Save(MockScript{ID: "b"})
		list, _ = s.List()
		if len(list) != 2 {
			t.Fatalf("expected 2, got %d", len(list))
		}
	})

	t.Run("Delete_missing_returns_error", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		if err := s.Delete("missing"); err == nil {
			t.Fatal("expected error for deleting missing ID")
		}
	})

	t.Run("Delete_present", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		s.Save(MockScript{ID: "d1"})
		if err := s.Delete("d1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := s.Get("d1"); err == nil {
			t.Fatal("expected error after Delete")
		}
	})

	t.Run("LoadBuiltin_seeds_all_builtin_scripts", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		if err := s.LoadBuiltin(BuiltinMockScripts()); err != nil {
			t.Fatalf("LoadBuiltin: %v", err)
		}
		list, _ := s.List()
		// 21 个 case 脚本 + 1 个 tool-error keyword 回退脚本 = 22。
		if len(list) != 22 {
			t.Fatalf("expected 22 builtin scripts (21 cases + tool-error fallback), got %d", len(list))
		}
	})

	t.Run("LoadBuiltin_skips_empty_ID", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		scripts := []MockScript{{ID: "", CaseID: "skip"}, {ID: "keep", CaseID: "k"}}
		if err := s.LoadBuiltin(scripts); err != nil {
			t.Fatalf("LoadBuiltin: %v", err)
		}
		list, _ := s.List()
		if len(list) != 1 {
			t.Fatalf("expected 1 (empty ID skipped), got %d", len(list))
		}
	})

	t.Run("LoadBuiltin_overwrites_same_ID", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		s.LoadBuiltin([]MockScript{{ID: "x", CaseID: "old"}})
		s.LoadBuiltin([]MockScript{{ID: "x", CaseID: "new"}})
		r, _ := s.Get("x")
		if r.CaseID != "new" {
			t.Fatalf("expected overwrite to new, got %q", r.CaseID)
		}
	})
}

// TestMockProviderImplementsProvider 是编译期检查，确保 MockProvider
// 满足导出的 Provider 接口（白盒访问该接口）。
func TestMockProviderImplementsProvider(t *testing.T) {
	var _ Provider = (*MockProvider)(nil)
	var _ Provider = NewMockProvider("mock", NewInMemoryMockScriptStore(), nil)
}

// TestBuiltinMockScriptsShape 对内置脚本集做 sanity check。
func TestBuiltinMockScriptsShape(t *testing.T) {
	scripts := BuiltinMockScripts()
	// 21 个 case 脚本 + 1 个 tool-error keyword 回退脚本 = 22。
	if len(scripts) != 22 {
		t.Fatalf("expected 22 builtin scripts (21 cases + tool-error fallback), got %d", len(scripts))
	}
	seen := map[string]bool{}
	for _, s := range scripts {
		if s.ID == "" {
			t.Fatal("builtin script has empty ID")
		}
		if s.CaseID == "" {
			t.Fatalf("builtin %s has empty CaseID", s.ID)
		}
		if len(s.Responses) == 0 {
			t.Fatalf("builtin %s has no responses", s.ID)
		}
		seen[s.CaseID] = true
	}
	// 21 个 case 的 CaseID 必须各自存在，外加 tool-error keyword 回退脚本。
	wantCaseIDs := []string{
		// L1
		"code-gen", "dialogue", "research", "long-task",
		// L2
		"todo-driven", "web-research", "skill-code-helper", "cron-notify", "llm-judge-qa",
		// L3
		"policy-enforcement", "approval-flow", "max-steps-exhaustion", "context-compression", "checkpoint-resume",
		// L4
		"multi-agent", "multi-agent-parallel", "multi-agent-sequential", "multi-agent-dag",
		// L5
		"multi-agent-leader-dispatch", "multi-agent-review", "multi-agent-fault-tolerance",
		// keyword 回退
		"tool-error",
	}
	for _, want := range wantCaseIDs {
		if !seen[want] {
			t.Fatalf("missing builtin CaseID %q", want)
		}
	}
}

// TestChatStreamOnChunk 验证 onChunk 回调会被以脚本化 delta 调用，
// 对 text 与 tool_call 两种响应类型都有效。
func TestChatStreamOnChunk(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	script := MockScript{
		ID:       "chunk",
		CaseID:   "chunk",
		Priority: 1000,
		Responses: []MockResponse{
			{Type: MockResponseText, Content: "hello-chunk"},
		},
	}
	store.Save(script)

	p := NewMockProvider("mock", store, nil)

	var collected []string
	_, _, _, err := p.ChatStream(ChatRequest{
		CaseID:   "chunk",
		Messages: []Message{{Role: "user", Content: "go"}},
	}, func(c StreamChunk) error {
		collected = append(collected, c.Delta.Content)
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if len(collected) != 1 || collected[0] != "hello-chunk" {
		t.Fatalf("expected [hello-chunk], got %v", collected)
	}
}

// TestChatStreamOnChunkError 验证 onChunk 返回的 error 会中止 stream。
func TestChatStreamOnChunkError(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	script := MockScript{
		ID:       "chunk-err",
		CaseID:   "chunk-err",
		Priority: 1000,
		Responses: []MockResponse{
			{Type: MockResponseText, Content: "x"},
		},
	}
	store.Save(script)

	p := NewMockProvider("mock", store, nil)
	sentinel := errors.New("callback-failed")
	_, _, _, err := p.ChatStream(ChatRequest{
		CaseID:   "chunk-err",
		Messages: []Message{{Role: "user", Content: "go"}},
	}, func(c StreamChunk) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// TestEmptyResponsesReturnsError 验证 responses 为空的脚本会返回
// 一个有意义的 error，而不是 panic。
func TestEmptyResponsesReturnsError(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	script := MockScript{
		ID:        "empty",
		CaseID:    "empty",
		Priority:  1000,
		Responses: nil,
	}
	store.Save(script)
	p := NewMockProvider("mock", store, nil)

	_, err := p.Chat(ChatRequest{
		CaseID:   "empty",
		Messages: []Message{{Role: "user", Content: "go"}},
	})
	if err == nil {
		t.Fatal("expected error for empty responses")
	}
	if !strings.Contains(err.Error(), "no responses") {
		t.Fatalf("error should mention 'no responses', got %q", err.Error())
	}
}

// TestName 验证 Name 访问器。
func TestName(t *testing.T) {
	p := NewMockProvider("my-mock", NewInMemoryMockScriptStore(), nil)
	if p.Name() != "my-mock" {
		t.Fatalf("expected my-mock, got %q", p.Name())
	}
}

// TestChatResponseFinishReason 验证脚本响应携带 tool call 时
// finish_reason 为 "tool_calls"，否则为 "stop"。
func TestChatResponseFinishReason(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	store.Save(MockScript{
		ID: "tc", CaseID: "tc", Priority: 1000,
		Responses: []MockResponse{
			{Type: MockResponseToolCall, ToolCalls: []ToolCall{{Idx: 0, ID: "c", Type: "function", Function: FunctionCall{Name: "run_shell", Arguments: "{}"}}}},
		},
	})
	store.Save(MockScript{
		ID: "txt", CaseID: "txt", Priority: 1000,
		Responses: []MockResponse{{Type: MockResponseText, Content: "hi"}},
	})
	p := NewMockProvider("mock", store, nil)

	resp, _ := p.Chat(ChatRequest{CaseID: "tc", Messages: []Message{{Role: "user", Content: "x"}}})
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("expected tool_calls, got %q", resp.Choices[0].FinishReason)
	}
	resp, _ = p.Chat(ChatRequest{CaseID: "txt", Messages: []Message{{Role: "user", Content: "x"}}})
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("expected stop, got %q", resp.Choices[0].FinishReason)
	}
}
