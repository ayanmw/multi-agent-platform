// Package llm — AnthropicProvider: Anthropic Claude Messages API implementation.
//
// Anthropic's protocol differs significantly from OpenAI-compatible APIs:
//   - Endpoint: POST /v1/messages (not /v1/chat/completions)
//   - System prompt: top-level "system" field (not a "system" role message)
//   - Tool schema: "input_schema" (not "parameters")
//   - Tool choice: object {"type": "..."} (not string "auto"/"none")
//   - max_tokens: REQUIRED (not optional)
//   - Auth: x-api-key header (not Bearer token)
//   - Streaming: double-layer SSE with "event:" type indicator
//   - Tool use streaming: content_block_start / content_block_delta with partial_json
//   - Token counting: input_tokens in message_start, output_tokens in message_delta
//
// # Thread Safety
//
// AnthropicProvider is safe for concurrent use — each call creates its own
// HTTP request and response. The underlying http.Client is goroutine-safe.
package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Anthropic-specific request/response types (not exported — internal conversion)
// ---------------------------------------------------------------------------

// anthropicMessage represents a single message in Anthropic's Messages API.
// Roles are: "user", "assistant". "system" is extracted to the top-level field.
type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

// anthropicContent is a polymorphic content block within a message.
// It can be plain text or a tool use block.
type anthropicContent struct {
	Type string `json:"type"` // "text" or "tool_use"

	// For text blocks:
	Text string `json:"text,omitempty"`

	// For tool_use blocks:
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	InputJSON map[string]interface{} `json:"input,omitempty"`

	// For tool_use outcome blocks (tool-result sent to Anthropic):
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

// anthropicChatRequest is the request body for POST /v1/messages.
type anthropicChatRequest struct {
	Model       string               `json:"model"`
	MaxTokens   int                  `json:"max_tokens"` // REQUIRED by Anthropic
	System      string               `json:"system,omitempty"`
	Messages    []anthropicMessage   `json:"messages"`
	Tools       []anthropicToolDef   `json:"tools,omitempty"`
	ToolChoice  *anthropicToolChoice `json:"tool_choice,omitempty"`
	Temperature float32              `json:"temperature,omitempty"`
	Stream      bool                 `json:"stream"`
}

// anthropicToolDef defines a tool in Anthropic's format.
// Key difference: "input_schema" instead of "parameters".
type anthropicToolDef struct {
	Type        string                 `json:"type"` // always "function"
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"` // JSON Schema (not "parameters")
}

// anthropicToolChoice specifies tool selection behavior in Anthropic's object format.
type anthropicToolChoice struct {
	Type string `json:"type"`           // "auto", "any", "tool", "none"
	Name string `json:"name,omitempty"` // used when Type == "tool"
}

// ---------------------------------------------------------------------------
// Anthropic streaming response types
// ---------------------------------------------------------------------------

// anthropicStreamEvent is the top-level SSE envelope for Anthropic.
// The event type is in the "event:" line, not in the JSON data.
type anthropicStreamEvent struct {
	Type string `json:"type"` // message_start, content_block_start, content_block_delta, etc.
}

// messageStartEvent carries initial message metadata including usage.
type messageStartEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Usage struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

// contentBlockStartEvent signals the beginning of a content block.
type contentBlockStartEvent struct {
	Type         string                      `json:"type"`
	Index        int                         `json:"index"`
	ContentBlock anthropicStreamContentBlock `json:"content_block"`
}

// anthropicStreamContentBlock is an initialized content block in streaming.
type anthropicStreamContentBlock struct {
	Type  string   `json:"type"` // "text" or "tool_use"
	Text  string   `json:"text,omitempty"`
	ID    string   `json:"id,omitempty"`
	Name  string   `json:"name,omitempty"`
	Input struct{} `json:"input,omitempty"` // empty — input comes via deltas
}

// contentBlockDeltaEvent carries an incremental delta for the current content block.
type contentBlockDeltaEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type string `json:"type"` // "text_delta" or "input_json_delta"
		Text string `json:"text,omitempty"`
		// PartialJSON is the incremental JSON fragment for tool_use argument streaming.
		PartialJSON string `json:"partial_json,omitempty"`
	} `json:"delta"`
}

// messageDeltaEvent signals the end of the message with final usage and stop reason.
type messageDeltaEvent struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"` // "end_turn", "tool_use", "max_tokens"
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// messageStopEvent marks the end of the stream.
type messageStopEvent struct {
	Type string `json:"type"`
}

// ---------------------------------------------------------------------------
// AnthropicProvider — implementation of the Provider interface
// ---------------------------------------------------------------------------

// AnthropicProvider implements the Provider interface for Anthropic's Claude Messages API.
//
// All Anthropic-specific format conversion happens behind this provider — the Engine
// sees only the unified ChatRequest/StreamChunk/Usage/ToolCall types.
type AnthropicProvider struct {
	name     string       // provider identifier, e.g. "anthropic"
	endpoint string       // base URL, e.g. "https://api.anthropic.com" (no /v1)
	apiKey   string       // Anthropic API key (x-api-key)
	model    string       // default model, e.g. "claude-sonnet-4-20250514"
	http     *http.Client // goroutine-safe
}

// NewAnthropicProvider creates a new AnthropicProvider.
//
// The endpoint should be the base URL (e.g. "https://api.anthropic.com") —
// the provider appends "/v1/messages" internally.
func NewAnthropicProvider(name, endpoint, apiKey, model string) *AnthropicProvider {
	endpoint = strings.TrimRight(endpoint, "/")
	// Strip trailing /v1 if provided so we can append it consistently.
	endpoint = strings.TrimSuffix(endpoint, "/v1")
	return &AnthropicProvider{
		name:     name,
		endpoint: endpoint,
		apiKey:   apiKey,
		model:    model,
		http:     &http.Client{Timeout: 120 * time.Second},
	}
}

// Name returns the provider identifier.
func (p *AnthropicProvider) Name() string {
	return p.name
}

// ---------------------------------------------------------------------------
// Chat — non-streaming request and response
// ---------------------------------------------------------------------------

// Chat sends a non-streaming chat request to the Anthropic Messages API
// and returns the fully parsed response.
//
// Format conversion:
//   - Extracts "system" role messages to the top-level "system" field
//   - Converts tool schemas: function.parameters → input_schema
//   - Converts tool_choice: string → {"type": "..."} object
//   - Sets max_tokens to 4096 if not already set (Anthropic requires it)
func (p *AnthropicProvider) Chat(req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	anthropicReq, err := p.buildAnthropicReq(req)
	if err != nil {
		return nil, fmt.Errorf("build anthropic request: %w", err)
	}

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(req.Context, "POST", p.endpoint+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, bodyBytes)
	}

	// Parse Anthropic's non-streaming response into unified ChatResponse.
	var anthropicResp struct {
		Content []struct {
			Type  string                 `json:"type"` // "text" or "tool_use"
			Text  string                 `json:"text,omitempty"`
			ID    string                 `json:"id,omitempty"`
			Name  string                 `json:"name,omitempty"`
			Input map[string]interface{} `json:"input,omitempty"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Convert Anthropic content blocks to unified Response.
	chatResp := &ChatResponse{
		ID: "anthropic-response",
		Choices: []Choice{{
			Index:        0,
			FinishReason: mapAnthropicStopReason(anthropicResp.StopReason),
			Message: Message{
				Role: "assistant",
			},
		}},
		Usage: Usage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
		},
	}

	// Reconstruct message content from content blocks.
	var textContent strings.Builder
	var toolCalls []ToolCall
	for _, block := range anthropicResp.Content {
		switch block.Type {
		case "text":
			textContent.WriteString(block.Text)
		case "tool_use":
			// Serialize input_map to JSON for the unified ToolCall.Arguments field.
			argsJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	chatResp.Choices[0].Message.Content = textContent.String()
	chatResp.Choices[0].Message.ToolCalls = toolCalls
	return chatResp, nil
}

// ---------------------------------------------------------------------------
// ChatStream — streaming request with double-layer SSE parsing
// ---------------------------------------------------------------------------

// ChatStream sends a streaming chat request to the Anthropic Messages API
// and calls onChunk for each SSE event.
//
// Anthropic's SSE format uses TWO separate header lines:
//   event: <event_type>
//   data: {"type": "<event_type>", ...}
//
// Event flow:
//   1. message_start        — extract input_tokens
//   2. content_block_start  — initialize text or tool_use block
//   3. content_block_delta  — accumulate text_delta or input_json_delta
//   4. message_delta        — extract output_tokens and stop_reason
//   5. message_stop         — stream complete
//
// Tool use arguments arrive as incremental JSON fragments (partial_json),
// not as complete JSON objects, so we accumulate them in a strings.Builder
// per content block index and parse only at the end.
func (p *AnthropicProvider) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	anthropicReq, err := p.buildAnthropicReq(req)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("build anthropic request: %w", err)
	}

	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(req.Context, "POST", p.endpoint+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return "", Usage{}, nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", Usage{}, nil, fmt.Errorf("API error %d: %s", resp.StatusCode, bodyBytes)
	}

	var (
		contentBuilder strings.Builder
		usage          Usage
		// Track the content block index that each tool call belongs to.
		// Anthropic sends deltas with an "index" field identifying the content block.
		toolCallMap = make(map[int]*ToolCall)
		// Accumulate partial_json for tool_use blocks until block is complete.
		toolArgBuilder = make(map[int]*strings.Builder)
		// Track which content blocks are tool_use (vs text).
		isToolBlock = make(map[int]bool)
		// Track tool name and ID for each tool_use block.
		toolBlockMeta = make(map[int]struct{ name, id string })
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Anthropic's SSE uses two-line events: "event: <type>\n data: {...}\n"
	// We need to buffer the event type between lines.
	var currentEventType string

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line separates SSE events — reset event type.
		if line == "" {
			currentEventType = ""
			continue
		}

		// Skip comments.
		if strings.HasPrefix(line, ":") {
			continue
		}

		// "event: <type>" line — capture the event type for the next data line.
		if strings.HasPrefix(line, "event: ") {
			currentEventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		// "data: {...}" line — process according to currentEventType.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		// Parse the data as a generic envelope to get the type field.
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			continue // skip malformed chunks
		}

		switch currentEventType {
		case "message_start":
			// Extract input_tokens from usage.
			var ev messageStartEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				usage.PromptTokens = ev.Usage.InputTokens
			}

		case "content_block_start":
			// A new content block begins — record its type and metadata.
			var ev contentBlockStartEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				switch ev.ContentBlock.Type {
				case "text":
					// Text block — no special setup needed.
				case "tool_use":
					isToolBlock[ev.Index] = true
					toolBlockMeta[ev.Index] = struct{ name, id string }{
						name: ev.ContentBlock.Name,
						id:   ev.ContentBlock.ID,
					}
					toolArgBuilder[ev.Index] = &strings.Builder{}
					// Initialize tool call on first block start.
					if _, exists := toolCallMap[ev.Index]; !exists {
						toolCallMap[ev.Index] = &ToolCall{
							Type: "function",
							Function: FunctionCall{
								Name:      ev.ContentBlock.Name,
								Arguments: "",
							},
						}
					}
				}
			}

		case "content_block_delta":
			// Incremental delta for the current content block.
			var ev contentBlockDeltaEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				switch ev.Delta.Type {
				case "text_delta":
					contentBuilder.WriteString(ev.Delta.Text)
				case "input_json_delta":
					// Accumulate partial JSON for tool_use arguments.
					if idx, ok := toolArgBuilder[ev.Index]; ok {
						idx.WriteString(ev.Delta.PartialJSON)
					}
				}
			}

		case "message_delta":
			// Extract output_tokens and stop reason.
			var ev messageDeltaEvent
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				usage.CompletionTokens = ev.Usage.OutputTokens
				// Finalize tool call arguments from accumulated partial_json.
				p.finalizeToolCalls(toolCallMap, toolArgBuilder, isToolBlock)
				// Emit the final StreamChunk so the Engine sees the finish reason.
				if onChunk != nil {
					sc := StreamChunk{
						FinishReason: mapAnthropicStopReason(ev.Delta.StopReason),
					}
					if err := onChunk(sc); err != nil {
						return contentBuilder.String(), usage, nil, err
					}
				}
			}

		case "message_stop":
			// Stream ended — compute total tokens.
			if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}
			// Finalize tool calls one last time (safety fallback).
			p.finalizeToolCalls(toolCallMap, toolArgBuilder, isToolBlock)

		case "error":
			// Handle error events.
			var errEv struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &errEv); err == nil {
				return contentBuilder.String(), usage, nil,
					fmt.Errorf("anthropic error (%s): %s", errEv.Error.Type, errEv.Error.Message)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return contentBuilder.String(), usage, nil, fmt.Errorf("scan stream: %w", err)
	}

	// Assemble tool calls from the map in index order.
	var toolCalls []ToolCall
	for i := 0; i < len(toolCallMap); i++ {
		if tc, ok := toolCallMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	return contentBuilder.String(), usage, toolCalls, nil
}

// ---------------------------------------------------------------------------
// Helper: request building
// ---------------------------------------------------------------------------

// buildAnthropicReq converts a unified ChatRequest into Anthropic's request format.
// It handles:
//   - Extracting system messages to top-level system field
//   - Converting tool definitions (input_schema)
//   - Converting tool_choice to object format
//   - Setting default max_tokens
func (p *AnthropicProvider) buildAnthropicReq(req ChatRequest) (anthropicChatRequest, error) {
	// Set default max_tokens if not provided (REQUIRED by Anthropic).
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	// Build system field from messages with Role == "system".
	var systemPrompt strings.Builder
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			if systemPrompt.Len() > 0 {
				systemPrompt.WriteString("\n\n")
			}
			systemPrompt.WriteString(msg.Content)
		}
	}

	// Convert messages, filtering out system messages (already extracted).
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return anthropicChatRequest{}, fmt.Errorf("convert messages: %w", err)
	}

	// Convert tool definitions.
	tools := convertTools(req.Tools)

	// Convert tool_choice.
	var toolChoice *anthropicToolChoice
	if req.ToolChoice != "" {
		toolChoice = convertToolChoice(req.ToolChoice, req.Tools)
	}

	return anthropicChatRequest{
		Model:       firstNonEmpty(req.Model, p.model),
		MaxTokens:   maxTokens,
		System:      systemPrompt.String(),
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  toolChoice,
		Temperature: req.Temperature,
		Stream:      req.Stream,
	}, nil
}

// ---------------------------------------------------------------------------
// Helper: tool definition conversion
// ---------------------------------------------------------------------------

// convertTools converts unified ToolDef slice to Anthropic's tool format.
// Key mapping: function.parameters → input_schema
func convertTools(tools []ToolDef) []anthropicToolDef {
	result := make([]anthropicToolDef, len(tools))
	for i, tool := range tools {
		result[i] = anthropicToolDef{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Helper: message conversion
// ---------------------------------------------------------------------------

// convertMessages converts unified Message slice to Anthropic's message format.
//
// Key conversions:
//   - "system" role messages: excluded (extracted to top-level system field by caller)
//   - "user" role messages: content becomes a single "text" block
//   - "assistant" role messages with tool_calls: content_blocks with type "tool_use"
//   - "tool" role messages: role stays "user" with content_blocks of type "tool_result"
//
// Anthropic requires tool-result messages to use role "user" (not "tool")
// and wrap each result in a content_block of type "tool_result" with tool_use_id.
func convertMessages(messages []Message) ([]anthropicMessage, error) {
	result := make([]anthropicMessage, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			// Skip — handled via top-level system field.
			continue

		case "user":
			// Plain user message — single text content block.
			am := anthropicMessage{
				Role: "user",
				Content: []anthropicContent{
					{Type: "text", Text: msg.Content},
				},
			}
			// Handle multi-part user content (file uploads, images) if Content
			// contains structured data. For now we treat everything as text.
			result = append(result, am)

		case "assistant":
			am := anthropicMessage{Role: "assistant"}
			// If the message has tool_calls, output tool_use blocks.
			if len(msg.ToolCalls) > 0 {
				am.Content = make([]anthropicContent, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					// Parse the JSON arguments into a map for Anthropic's input field.
					var argsMap map[string]interface{}
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &argsMap)
					am.Content[i] = anthropicContent{
						Type:      "tool_use",
						ID:        tc.ID,
						Name:      tc.Function.Name,
						InputJSON: argsMap,
					}
				}
			} else if msg.Content != "" {
				// Plain text assistant message.
				am.Content = []anthropicContent{
					{Type: "text", Text: msg.Content},
				}
			}
			result = append(result, am)

		case "tool":
			// Tool results must be role "user" with tool_result content blocks.
			am := anthropicMessage{
				Role: "user",
				Content: []anthropicContent{
					{
						Type:      "tool_result",
						ToolUseID: msg.ToolCallID,
						Content:   msg.Content,
					},
				},
			}
			result = append(result, am)
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Helper: tool_choice conversion
// ---------------------------------------------------------------------------

// convertToolChoice converts the unified string tool_choice to Anthropic's
// object format. OpenAI uses strings like "auto", "none", or a tool name.
// Anthropic uses: {"type": "auto"} | {"type": "any"} | {"type": "tool", "name": "..."}
func convertToolChoice(choice string, availableTools []ToolDef) *anthropicToolChoice {
	switch choice {
	case "auto":
		return &anthropicToolChoice{Type: "auto"}
	case "none":
		return &anthropicToolChoice{Type: "none"}
	default:
		// If it matches a tool name, use {"type": "tool", "name": "..."}.
		for _, tool := range availableTools {
			if tool.Function.Name == choice {
				return &anthropicToolChoice{Type: "tool", Name: choice}
			}
		}
		// Fallback to auto.
		return &anthropicToolChoice{Type: "auto"}
	}
}

// ---------------------------------------------------------------------------
// Helper: stream parsing
// ---------------------------------------------------------------------------

// finalizeToolCalls completes tool call argument assembly from accumulated
// partial_json deltas. Called at message_delta and message_stop to ensure
// all tool arguments are fully parsed before returning to the Engine.
func (p *AnthropicProvider) finalizeToolCalls(
	toolCallMap map[int]*ToolCall,
	toolArgBuilder map[int]*strings.Builder,
	isToolBlock map[int]bool,
) {
	for idx, builder := range toolArgBuilder {
		if builder == nil {
			continue
		}
		if tc, ok := toolCallMap[idx]; ok && isToolBlock[idx] {
			// The accumulated partial_json should be valid JSON.
			argsJSON := builder.String()
			// Validate by attempting to parse — if it fails, store raw string.
			var test map[string]interface{}
			if err := json.Unmarshal([]byte(argsJSON), &test); err == nil {
				tc.Function.Arguments = argsJSON
			} else if argsJSON != "" {
				// Wrap in a content field if not a valid JSON object.
				tc.Function.Arguments = fmt.Sprintf(`{"content": %s}`, argsJSON)
			}
		}
	}
}

// mapAnthropicStopReason converts Anthropic's stop reason strings to
// the unified finish reason format used across providers.
func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		return reason
	}
}

// firstNonEmpty returns the first non-empty string from the provided values.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
