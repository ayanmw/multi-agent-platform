// Package llm —— GeminiProvider：Google Gemini API 实现。
//
// Gemini 的协议与 OpenAI-compatible API 不同：
//   - Endpoint: POST /v1beta/models/{model}:generateContent（非流式）或
//     /v1beta/models/{model}:streamGenerateContent（流式）
//   - Auth: ?key=<API_KEY> query 参数，而非 header
//   - 请求体: {contents: [...], tools: [...], generationConfig: {...}}
//   - 消息 role: "user" / "model"（不是 "assistant"）
//   - Tool schema: 通过 "functionDeclarations" 包装
//
// 本实现为 Phase 7 multi-model 路由的 stub：提供完整的 Provider 接口实现、
// 统一类型的请求/响应转换与非流式请求。流式解析后续 Phase 补齐。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GeminiProvider 为 Google Gemini API 实现 Provider 接口。
//
// 它是多 model provider 生态的一员，让 Router 可以选择 Gemini 系列模型
//（如 gemini-3.1-pro-preview、gemini-3-flash-preview）。
//
// # 线程安全
//
// 每次 Chat/ChatStream 调用创建独立 HTTP request，可安全并发使用。
type GeminiProvider struct {
	name     string       // provider 标识，例如 "gemini"
	endpoint string       // 基础 URL，例如 "https://generativelanguage.googleapis.com/v1beta"
	apiKey   string       // API key，通过 query 参数传递
	model    string       // 默认 model 名
	http     *http.Client // goroutine 安全
}

// NewGeminiProvider 创建一个新的 GeminiProvider。
//
// endpoint 应为基础 URL（例如 "https://generativelanguage.googleapis.com/v1beta"）。
func NewGeminiProvider(name, endpoint, apiKey, model string) *GeminiProvider {
	return &GeminiProvider{
		name:     name,
		endpoint: strings.TrimRight(endpoint, "/"),
		apiKey:   apiKey,
		model:    model,
		http:     &http.Client{Timeout: 120 * time.Second},
	}
}

// Name 返回 provider 标识。
func (p *GeminiProvider) Name() string {
	return p.name
}

// Chat 向 Gemini API 发送非流式请求并返回统一 ChatResponse。
func (p *GeminiProvider) Chat(req ChatRequest) (*ChatResponse, error) {
	model := firstNonEmpty(req.Model, p.model)
	gReq, err := p.buildGeminiRequest(req)
	if err != nil {
		return nil, fmt.Errorf("build gemini request: %w", err)
	}

	body, err := json.Marshal(gReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", p.endpoint, model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(req.Context, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string                 `json:"name"`
						Args map[string]interface{} `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	chatResp := &ChatResponse{
		ID:      "gemini-response",
		Choices: []Choice{},
		Usage:   Usage{},
	}

	if len(geminiResp.Candidates) > 0 {
		cand := geminiResp.Candidates[0]
		var text strings.Builder
		var toolCalls []ToolCall
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				text.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				toolCalls = append(toolCalls, ToolCall{
					Type: "function",
					Function: FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}
		chatResp.Choices = append(chatResp.Choices, Choice{
			Index:        0,
			FinishReason: mapGeminiFinishReason(cand.FinishReason),
			Message: Message{
				Role:      "assistant",
				Content:   text.String(),
				ToolCalls: toolCalls,
			},
		})
	}
	if geminiResp.UsageMetadata.TotalTokenCount > 0 {
		chatResp.Usage = Usage{
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
		}
	} else if len(chatResp.Choices) > 0 {
		// 部分本地/代理 Gemini 部署不返回 usage，保守估算避免后续除零。
		chatResp.Usage.TotalTokens = len(chatResp.Choices[0].Message.Content)
	}

	return chatResp, nil
}

// ChatStream 当前尚未实现；返回错误以明确告知调用方。
// 后续 Phase 将补齐流式解析（streamGenerateContent 及 SSE-style 响应）。
func (p *GeminiProvider) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	return "", Usage{}, nil, fmt.Errorf("GeminiProvider.ChatStream not implemented")
}

// ListModels 当前返回空列表；Gemini 模型列表通过本地配置与缺省画像管理。
// 后续可接入 GET /v1beta/models 发现端点。
func (p *GeminiProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	log.Infof("llm", "[GeminiProvider] ListModels not yet implemented for provider %q", p.name)
	return []ModelInfo{}, nil
}

// buildGeminiRequest 将统一 ChatRequest 转换为 Gemini 格式。
func (p *GeminiProvider) buildGeminiRequest(req ChatRequest) (map[string]interface{}, error) {
	model := firstNonEmpty(req.Model, p.model)
	contents, err := convertMessagesToGemini(req.Messages)
	if err != nil {
		return nil, err
	}

	gReq := map[string]interface{}{
		"contents": contents,
		"generationConfig": map[string]interface{}{
			"temperature": req.Temperature,
			"maxOutputTokens": func() int {
				if req.MaxTokens > 0 {
					return req.MaxTokens
				}
				return 4096
			}(),
		},
	}
	_ = model
	if tools := convertToolsToGemini(req.Tools); len(tools) > 0 {
		gReq["tools"] = tools
	}

	return gReq, nil
}

// convertMessagesToGemini 将统一 Message slice 转换为 Gemini contents 格式。
//
//   - "system" role 消息合并为单条 user 消息前的 system_instruction（本 stub 中
//     简单追加到第一条 user content 中）。
//   - "assistant" role 映射为 "model"。
//   - "tool" role 消息转为 functionResponse part，并合并到前一条 user turn。
func convertMessagesToGemini(messages []Message) ([]map[string]interface{}, error) {
	var systemParts []string
	var contents []map[string]interface{}

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			systemParts = append(systemParts, msg.Content)
		case "user":
			parts := []map[string]interface{}{{"type": "text", "text": msg.Content}}
			contents = append(contents, map[string]interface{}{
				"role":  "user",
				"parts": parts,
			})
		case "assistant":
			parts := []map[string]interface{}{{"type": "text", "text": msg.Content}}
			contents = append(contents, map[string]interface{}{
				"role":  "model",
				"parts": parts,
			})
		case "tool":
			if len(contents) == 0 {
				contents = append(contents, map[string]interface{}{
					"role": "user",
					"parts": []map[string]interface{}{
						{"type": "text", "text": "tool result: " + msg.Content},
					},
				})
				continue
			}
			last := contents[len(contents)-1]
			parts, _ := last["parts"].([]map[string]interface{})
			parts = append(parts, map[string]interface{}{
				"functionResponse": map[string]interface{}{
					"name": msg.Name,
					"response": map[string]interface{}{
						"result": msg.Content,
					},
				},
			})
			contents[len(contents)-1]["parts"] = parts
		}
	}

	// 如果有 system prompt 但没有 user 消息，补一条空 user 消息承载 system。
	if len(systemParts) > 0 {
		systemText := strings.Join(systemParts, "\n\n")
		if len(contents) == 0 {
			contents = append(contents, map[string]interface{}{
				"role": "user",
				"parts": []map[string]interface{}{
					{"text": systemText},
				},
			})
		} else {
			// 将 system text 前置到第一条 user content 中。
			first := contents[0]
			firstRole, _ := first["role"].(string)
			if firstRole == "user" {
				parts, _ := first["parts"].([]map[string]interface{})
				parts = append([]map[string]interface{}{{"text": systemText}}, parts...)
				first["parts"] = parts
			}
		}
	}

	return contents, nil
}

// convertToolsToGemini 将统一 ToolDef slice 转换为 Gemini functionDeclarations 格式。
func convertToolsToGemini(tools []ToolDef) []map[string]interface{} {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		decls[i] = map[string]interface{}{
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
		}
	}
	return []map[string]interface{}{
		{"functionDeclarations": decls},
	}
}

// mapGeminiFinishReason 将 Gemini 的 finish reason 映射为统一格式。
func mapGeminiFinishReason(reason string) string {
	switch strings.ToLower(reason) {
	case "stop":
		return "stop"
	case "maxtokens":
		return "length"
	case "safety", "recitation":
		return "content_filter"
	default:
		return reason
	}
}
