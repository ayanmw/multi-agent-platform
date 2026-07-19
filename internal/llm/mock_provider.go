// Package llm —— MockProvider：用于测试的确定性 LLM provider。
//
// MockProvider 实现了 Provider 接口，但绝不调用远程 API。
// 而是从 MockScriptStore 中查找 MockScript，并返回脚本中预设的响应序列。
package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MockProvider 基于确定性脚本实现 Provider 接口。
//
// 它通过 case_id（来自 ChatRequest.CaseID）或对最后一条 user 消息的关键字
// 匹配来选择脚本，并重放脚本序列中的下一条响应。
// 内置脚本在启动时加载到 store 中，作为回退使用。
type MockProvider struct {
	name            string
	store           MockScriptStore
	builtinScripts  []MockScript
	callIndexByCase map[string]int
}

// NewMockProvider 创建一个以 store 为底层、以 builtinScripts 为
// 无动态脚本匹配时回退的新 mock provider。
func NewMockProvider(name string, store MockScriptStore, builtinScripts []MockScript) *MockProvider {
	return &MockProvider{
		name:            name,
		store:           store,
		builtinScripts:  builtinScripts,
		callIndexByCase: make(map[string]int),
	}
}

// Name 返回 provider 标识。
func (p *MockProvider) Name() string { return p.name }

// Chat 发送非流式请求并返回第一条脚本响应。
func (p *MockProvider) Chat(req ChatRequest) (*ChatResponse, error) {
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	content, usage, toolCalls, err := p.chatStream(ctx, req, nil)
	if err != nil {
		return nil, err
	}
	return &ChatResponse{
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: content, ToolCalls: toolCalls},
				FinishReason: finishReason(toolCalls),
			},
		},
		Usage: usage,
	}, nil
}

// ChatStream 发送流式请求并发出脚本化的 chunk。
func (p *MockProvider) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return p.chatStream(ctx, req, onChunk)
}

func (p *MockProvider) chatStream(ctx context.Context, req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	scripts, err := p.store.List()
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("list mock scripts: %w", err)
	}

	userInput := lastUserMessage(req.Messages)
	scriptKey, script := p.selectScript(userInput, req.Model, req.CaseID, scripts)
	if script.ID == "" {
		return "", Usage{}, nil, fmt.Errorf("no matching mock script for input: %s", userInput)
	}

	resp := script.Responses
	if len(resp) == 0 {
		return "", Usage{}, nil, fmt.Errorf("mock script %q has no responses", script.ID)
	}

	idx := p.callIndexByCase[scriptKey]
	if idx >= len(resp) {
		idx = len(resp) - 1
	}
	entry := resp[idx]
	p.callIndexByCase[scriptKey] = idx + 1

	usage := Usage{PromptTokens: len(userInput), CompletionTokens: len(entry.Content)}
	if entry.Type == MockResponseToolCall {
		usage.CompletionTokens = len(fmt.Sprintf("%v", entry.ToolCalls))
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	if entry.DelayMs > 0 {
		timer := time.NewTimer(time.Duration(entry.DelayMs) * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return "", Usage{}, nil, ctx.Err()
		}
	}

	switch entry.Type {
	case MockResponseText:
		if onChunk != nil {
			if err := onChunk(StreamChunk{Delta: Delta{Content: entry.Content}}); err != nil {
				return "", Usage{}, nil, err
			}
		}
		return entry.Content, usage, nil, nil
	case MockResponseToolCall:
		if onChunk != nil {
			if err := onChunk(StreamChunk{Delta: Delta{ToolCalls: entry.ToolCalls}}); err != nil {
				return "", Usage{}, nil, err
			}
		}
		return "", usage, entry.ToolCalls, nil
	default:
		return "", Usage{}, nil, fmt.Errorf("unknown mock response type: %q", entry.Type)
	}
}

// selectScript 为请求选择最佳匹配脚本。
// 它返回用于响应序列索引的稳定 key 以及被选中的脚本。
func (p *MockProvider) selectScript(userInput, model, caseID string, scripts []MockScript) (string, MockScript) {
	lowerInput := strings.ToLower(userInput)

	// 来自 store 的动态脚本优先。
	allScripts := append([]MockScript{}, scripts...)
	// 内置脚本追加在后作为回退。
	allScripts = append(allScripts, p.builtinScripts...)

	var best MockScript
	bestScore := -1
	bestKey := ""
	for _, script := range allScripts {
		score := 0
		key := "case:" + script.CaseID
		if script.CaseID != "" && (strings.EqualFold(script.CaseID, caseID) || strings.Contains(lowerInput, strings.ToLower(script.CaseID))) {
			score += 1000
		}
		for _, kw := range script.MatchInput {
			if strings.Contains(lowerInput, strings.ToLower(kw)) {
				score += 10
				key = "keyword:" + script.ID
			}
		}
		if model != "" && script.CaseID != "" && strings.Contains(strings.ToLower(model), strings.ToLower(script.CaseID)) {
			score += 5
		}
		score += script.Priority
		if score > bestScore {
			bestScore = score
			best = script
			bestKey = key
		}
	}
	return bestKey, best
}

func lastUserMessage(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

func finishReason(toolCalls []ToolCall) string {
	if len(toolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}

var _ Provider = (*MockProvider)(nil)
