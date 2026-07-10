package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestSelectScriptCaseIDMatch verifies that selectScript (exercised via Chat)
// picks the script whose CaseID equals ChatRequest.CaseID. Builtins are omitted
// so this isolates case_id matching from priority/keyword scoring.
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

// TestSelectScriptKeywordFallback verifies that when CaseID is empty but the
// user message contains a script's MatchInput keyword, the script is selected.
func TestSelectScriptKeywordFallback(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	scripts := []MockScript{
		{
			ID:         "kw-weather",
			CaseID:     "weather", // CaseID present, but request.CaseID is empty
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

// TestDynamicScriptOverridesBuiltin verifies that a dynamic script with the
// same CaseID as a builtin but higher Priority wins (dynamic scripts are
// appended before builtins in selectScript, and priority is added to the score).
func TestDynamicScriptOverridesBuiltin(t *testing.T) {
	store := NewInMemoryMockScriptStore()
	dyn := MockScript{
		ID:       "dyn:dialogue",
		CaseID:   "dialogue",
		Priority: 500, // higher than builtin's 100
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

// TestResponseSequenceProgression verifies that two consecutive ChatStream
// calls on the same case return responses[0] then responses[1], and that a
// third call clamps to the last response.
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

	// First call: tool_call response.
	content, _, toolCalls, err := p.ChatStream(req, nil)
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if content != "" || len(toolCalls) != 1 || toolCalls[0].ID != "c1" {
		t.Fatalf("call 1: expected tool_call c1, got content=%q toolCalls=%v", content, toolCalls)
	}

	// Second call: text response.
	content, _, toolCalls, err = p.ChatStream(req, nil)
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if content != "final-text" || len(toolCalls) != 0 {
		t.Fatalf("call 2: expected final-text, got content=%q toolCalls=%v", content, toolCalls)
	}

	// Third call: clamps to last response (text).
	content, _, toolCalls, err = p.ChatStream(req, nil)
	if err != nil {
		t.Fatalf("call 3: %v", err)
	}
	if content != "final-text" || len(toolCalls) != 0 {
		t.Fatalf("call 3: expected clamp to final-text, got content=%q toolCalls=%v", content, toolCalls)
	}
}

// TestUsageCalculation verifies the Usage fields for text and tool_call responses.
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

	// Tool-call script with known ToolCalls string representation.
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

	// Text response: PromptTokens = len(userInput), CompletionTokens = len(content).
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

	// Tool-call response: CompletionTokens = len(fmt.Sprintf("%v", ToolCalls)).
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

// TestNoMatchingScriptReturnsError verifies that an empty store with no
// keyword-matching input and empty CaseID yields an error mentioning
// "no matching mock script".
func TestNoMatchingScriptReturnsError(t *testing.T) {
	store := NewInMemoryMockScriptStore() // empty
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

// TestDelayMsZeroFastPath verifies that DelayMs=0 does not block.
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

// TestDelayMsHonorsContextCancel verifies that a positive DelayMs respects
// context cancellation.
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

// TestInMemoryMockScriptStoreCRUD covers Save/Get/List/Delete and LoadBuiltin.
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

	t.Run("LoadBuiltin_seeds_six_scripts", func(t *testing.T) {
		s := NewInMemoryMockScriptStore()
		if err := s.LoadBuiltin(BuiltinMockScripts()); err != nil {
			t.Fatalf("LoadBuiltin: %v", err)
		}
		list, _ := s.List()
		if len(list) != 6 {
			t.Fatalf("expected 6 builtin scripts, got %d", len(list))
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

// TestMockProviderImplementsProvider is a compile-time check that MockProvider
// satisfies the exported Provider interface (white-box access to the interface).
func TestMockProviderImplementsProvider(t *testing.T) {
	var _ Provider = (*MockProvider)(nil)
	var _ Provider = NewMockProvider("mock", NewInMemoryMockScriptStore(), nil)
}

// TestBuiltinMockScriptsShape sanity-checks the built-in script set.
func TestBuiltinMockScriptsShape(t *testing.T) {
	scripts := BuiltinMockScripts()
	if len(scripts) != 6 {
		t.Fatalf("expected 6 builtin scripts, got %d", len(scripts))
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
	for _, want := range []string{"code-gen", "dialogue", "research", "multi-agent", "long-task", "tool-error"} {
		if !seen[want] {
			t.Fatalf("missing builtin CaseID %q", want)
		}
	}
}

// TestChatStreamOnChunk verifies the onChunk callback is invoked with the
// scripted delta for both text and tool_call response types.
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

// TestChatStreamOnChunkError verifies that an onChunk error aborts the stream.
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

// TestEmptyResponsesReturnsError verifies that a script with no responses
// yields a descriptive error rather than panicking.
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

// TestName verifies the Name accessor.
func TestName(t *testing.T) {
	p := NewMockProvider("my-mock", NewInMemoryMockScriptStore(), nil)
	if p.Name() != "my-mock" {
		t.Fatalf("expected my-mock, got %q", p.Name())
	}
}

// TestChatResponseFinishReason verifies finish_reason is "tool_calls" when the
// scripted response carries tool calls and "stop" otherwise.
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
