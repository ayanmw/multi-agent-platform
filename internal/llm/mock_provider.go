// Package llm — MockProvider: deterministic LLM provider for testing.
//
// MockProvider implements the Provider interface but never calls a remote API.
// Instead, it looks up a MockScript from a MockScriptStore and returns the
// scripted response sequence.
package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MockProvider implements the Provider interface using deterministic scripts.
//
// It selects a script by case_id (from ChatRequest.CaseID) or by keyword matching
// the last user message, and replays the next response in the script's sequence.
// Built-in scripts are loaded into the store at startup and serve as fallbacks.
type MockProvider struct {
	name            string
	store           MockScriptStore
	builtinScripts  []MockScript
	callIndexByCase map[string]int
}

// NewMockProvider creates a new mock provider backed by store, with builtinScripts
// available as fallback when no dynamic script matches.
func NewMockProvider(name string, store MockScriptStore, builtinScripts []MockScript) *MockProvider {
	return &MockProvider{
		name:            name,
		store:           store,
		builtinScripts:  builtinScripts,
		callIndexByCase: make(map[string]int),
	}
}

// Name returns the provider identifier.
func (p *MockProvider) Name() string { return p.name }

// Chat sends a non-streaming request and returns the first scripted response.
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

// ChatStream sends a streaming request and emits scripted chunks.
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

// selectScript chooses the best matching script for the request.
// It returns a stable key for response-sequence indexing and the selected script.
func (p *MockProvider) selectScript(userInput, model, caseID string, scripts []MockScript) (string, MockScript) {
	lowerInput := strings.ToLower(userInput)

	// Dynamic scripts from store take precedence.
	allScripts := append([]MockScript{}, scripts...)
	// Built-in scripts are appended as fallback.
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
