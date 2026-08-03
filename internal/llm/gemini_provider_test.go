package llm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGeminiProvider_ChatStream 验证 Gemini SSE 流式解析：
//   - 文本增量跨 chunk 累积为完整 content；
//   - functionCall part 收集为统一 ToolCall（name + 完整 args）；
//   - usage 取自最后一个含 usageMetadata 的 chunk；
//   - 每个文本 chunk 经 onChunk 实时回调（白盒）。
func TestGeminiProvider_ChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.RawQuery, "alt=sse") {
			t.Errorf("expected alt=sse in query, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// 三个 SSE data 行：两段文本增量 + 一个 functionCall（携带 usage）。
		chunks := []string{
			`{"candidates":[{"content":{"parts":[{"text":"Hello "}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"text":"world"}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"run_shell","args":{"cmd":"ls"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":7,"totalTokenCount":19}}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
	}))
	defer server.Close()

	p := NewGeminiProvider("gemini", server.URL, "test-key", "gemini-x")

	var deltas []string
	content, usage, toolCalls, err := p.ChatStream(ChatRequest{
		Model:  "gemini-x",
		Stream: true,
	}, func(sc StreamChunk) error {
		if sc.Delta.Content != "" {
			deltas = append(deltas, sc.Delta.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}

	if content != "Hello world" {
		t.Errorf("content = %q, want %q", content, "Hello world")
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Function.Name != "run_shell" {
		t.Errorf("tool name = %q, want run_shell", toolCalls[0].Function.Name)
	}
	if toolCalls[0].Function.Arguments != `{"cmd":"ls"}` {
		t.Errorf("tool args = %q, want %q", toolCalls[0].Function.Arguments, `{"cmd":"ls"}`)
	}
	if usage.TotalTokens != 19 || usage.PromptTokens != 12 || usage.CompletionTokens != 7 {
		t.Errorf("usage = %+v, want total=19 prompt=12 completion=7", usage)
	}
	if len(deltas) != 2 {
		t.Errorf("expected 2 content deltas, got %d (%v)", len(deltas), deltas)
	}
}

// TestGeminiProvider_ChatStream_TextOnly 验证纯文本流式（无 tool call、
// usage 在最终 chunk）正确累积并返回空 toolCalls。
func TestGeminiProvider_ChatStream_TextOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"candidates":[{"content":{"parts":[{"text":"1"}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"text":"2"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
	}))
	defer server.Close()

	p := NewGeminiProvider("gemini", server.URL, "k", "m")
	content, usage, toolCalls, err := p.ChatStream(ChatRequest{Model: "m"}, nil)
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}
	if content != "12" {
		t.Errorf("content = %q, want %q", content, "12")
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(toolCalls))
	}
	if usage.TotalTokens != 4 {
		t.Errorf("usage.TotalTokens = %d, want 4", usage.TotalTokens)
	}
}

// TestGeminiProvider_ChatStream_APIError 验证非 200 响应返回 error
// （不再是 stub 时期的 "not implemented"）。
func TestGeminiProvider_ChatStream_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer server.Close()

	p := NewGeminiProvider("gemini", server.URL, "bad", "m")
	_, _, _, err := p.ChatStream(ChatRequest{Model: "m"}, nil)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

// TestGeminiProvider_ChatStream_StreamError 验证流内错误 chunk（200 响应体内）
// 被识别为 error 而非当作正常结束。
func TestGeminiProvider_ChatStream_StreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"error\":{\"status\":\"PERMISSION_DENIED\",\"message\":\"API key expired\"}}\n\n")
	}))
	defer server.Close()

	p := NewGeminiProvider("gemini", server.URL, "bad", "m")
	_, _, _, err := p.ChatStream(ChatRequest{Model: "m"}, nil)
	if err == nil {
		t.Fatal("expected stream error to be returned")
	}
}
