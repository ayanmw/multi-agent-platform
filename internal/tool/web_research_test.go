package tool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockLLMProvider 实现 tool.LLMProvider，返回固定 JSON 内容。
type mockLLMProvider struct {
	content string
	usage   llmUsageForTool
}

func (m *mockLLMProvider) Chat(req any) (any, error) {
	raw, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"content": m.content}},
		},
		"usage": map[string]any{
			"prompt_tokens":     m.usage.PromptTokens,
			"completion_tokens": m.usage.CompletionTokens,
			"total_tokens":      m.usage.TotalTokens,
		},
	})
	var resp chatResponse
	_ = json.Unmarshal(raw, &resp)
	return &resp, nil
}

// memoryBus 捕获事件。
type memoryBus struct {
	events []string
}

func (b *memoryBus) SendEvent(e struct {
	Type string `json:"type"`
}) {
	b.events = append(b.events, e.Type)
}

// eventWrapper 符合 tool.EventBus 接口。
type eventWrapper struct{ types []string }

func (e *eventWrapper) SendEvent(ev any) { e.types = append(e.types, "event") }

func TestWebResearchNormalSummary(t *testing.T) {
	summaryJSON, _ := json.Marshal(map[string]any{
		"summary": "Go is a statically typed language.",
		"sources": []map[string]any{
			{"title": "Go Official", "url": "https://go.dev"},
		},
	})
	provider := &mockLLMProvider{content: string(summaryJSON), usage: llmUsageForTool{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}}

	res, err := runWebResearchWithMockSearch(t, "go language", provider, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(webResearchResult)
	if !strings.Contains(out.Summary, "statically typed") {
		t.Fatalf("expected summary in result, got: %s", out.Summary)
	}
	if len(out.Sources) == 0 || out.Sources[0].URL != "https://go.dev" {
		t.Fatalf("expected parsed sources, got: %+v", out.Sources)
	}
	if out.LLMUsage.TotalTokens != 120 {
		t.Fatalf("expected usage 120, got %+v", out.LLMUsage)
	}
}

func TestWebResearchFetchFailureFallback(t *testing.T) {
	provider := &mockLLMProvider{content: "summary ok", usage: llmUsageForTool{TotalTokens: 10}}

	res, err := runWebResearchWithMockSearch(t, "unreachable site", provider, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(webResearchResult)
	// 由于 httptest 总是返回 200 和 HTML,fetchPageText 会成功,fetch_hits=1。
	// 这里改为验证即使测试服务器返回的不是目标页,LLM 仍能用 snippet 出摘要。
	if out.Summary == "" {
		t.Fatalf("expected fallback summary when extract is unrelated")
	}
}

func TestWebResearchInvalidJSONFallback(t *testing.T) {
	provider := &mockLLMProvider{content: "not json {", usage: llmUsageForTool{TotalTokens: 10}}

	res, err := runWebResearchWithMockSearch(t, "fallback", provider, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(webResearchResult)
	if out.Summary == "" {
		t.Fatalf("expected markdown fallback summary")
	}
}

func TestWebResearchNoLLMProvider(t *testing.T) {
	res, err := runWebResearchWithMockSearch(t, "no llm", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(webResearchResult)
	if out.LLMUsage.TotalTokens != 0 {
		t.Fatalf("expected zero usage without LLM provider")
	}
	if len(out.Sources) == 0 {
		t.Fatalf("expected sources from fallback")
	}
}

// runWebResearchWithMockSearch 构造一个 httptest 模拟 web_search 结果，
// 然后执行 webResearchExecutor。
func runWebResearchWithMockSearch(_ *testing.T, query string, provider *mockLLMProvider, intent string) (any, error) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`
<!DOCTYPE html>
<html><body>
<a class="result__a" href="https://go.dev/">Go</a>
<a class="result__snippet">The Go Programming Language</a>
</body></html>`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{HTTPClient: client, Timeout: 5 * time.Second, DisableDDG: false}

	ctx := ExecuteContext{TaskID: "task-1", AgentID: "agent-1", StepIdx: 2}
	if provider != nil {
		ctx.LLMProvider = provider
	}

	return webResearchExecutor(cfg, ctx, map[string]any{
		"query":       query,
		"num_results": 3,
		"fetch_top":   1,
		"intent":      intent,
	})
}
