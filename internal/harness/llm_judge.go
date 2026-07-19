// Package harness —— LLM Judge：用 LLM 按 rubric 对 agent 输出做语义评估。
//
// 确定性 acceptance criteria（file_exists、content_contains、shell_exit_zero）在
// harness.go 中实现。LLMJudge 作为补充，允许 contract 指定一段自由文本 rubric，由 LLM
// 针对 agent 的 final answer 与近期 tool 输出进行评估。
//
// # 设计要点
//
//   - judge 使用专用的非流式 chat 调用，因此可在任务完成时运行一次，不会增加 ReAct loop
//     的延迟。
//   - 要求 LLM 返回严格的 JSON 对象；解析器容忍常见包装（markdown 代码围栏、多余空白），
//     当响应不是合法 JSON 时优雅降级。
//   - 无论模型返回什么，分数都会被 clamp 到 [0, 1]。
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// JudgeRequest 包含 judge 评估一次 agent 运行所需的一切。
type JudgeRequest struct {
	// Goal 是来自 TaskContract 的高层目标。
	Goal string

	// Rubric 是 criterion 专用的问题或评分指南。
	Rubric string

	// UserInput 是启动该任务的原始用户请求。
	UserInput string

	// FinalAnswer 是 agent 在任务完成时产出的 final answer。
	FinalAnswer string

	// ToolOutputs 是 agent 观察到的最后 N 条重要 tool 结果。它们为 judge 提供证据，
	// 但可选。
	ToolOutputs []string
}

// JudgeResult 是 LLM judge 评估的结构化输出。
type JudgeResult struct {
	Passed bool    `json:"passed"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// judgeChatClient 是 LLMJudge 所需的 LLM 层最小能力。它匹配 llm.Provider.Chat，因此
// 任何 provider 都可提供，而无需依赖流式内部实现。
type judgeChatClient interface {
	Chat(req llm.ChatRequest) (*llm.ChatResponse, error)
}

// LLMJudge 使用 LLM 按 rubric 评估 agent 输出。
//
// 它刻意依赖较小的 judgeChatClient 接口而非完整的 llm.Router，以便单元测试可以提供
// stub，并使 judge 可锚定到特定模型（通常是便宜/高效的模型）。
type LLMJudge struct {
	client judgeChatClient
	model  string
}

// NewLLMJudge 创建使用给定 provider 与 model 的 judge。
//
// 若 model 为空，使用 provider 的默认模型。provider 必须满足 llm.Provider；构造函数
// 直接接受 llm.Provider。
func NewLLMJudge(provider llm.Provider, model string) *LLMJudge {
	return &LLMJudge{client: provider, model: model}
}

// Evaluate 请求 LLM 判断 final answer 是否满足 rubric。
//
// 当模型输出无法解析为 JSON 时，返回 Passed=false/Score=0，并以原始响应作为 Reason，
// 因此格式错误的 judge 响应不会让 evaluator 崩溃。
func (j *LLMJudge) Evaluate(ctx context.Context, req JudgeRequest) (*JudgeResult, error) {
	prompt := buildJudgePrompt(req)
	chatReq := llm.ChatRequest{
		Model:       j.model,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		Temperature: 0,
		Stream:      false,
		Context:     ctx,
	}

	resp, err := j.client.Chat(chatReq)
	if err != nil {
		return nil, fmt.Errorf("judge LLM call failed: %w", err)
	}

	if resp == nil || len(resp.Choices) == 0 {
		return &JudgeResult{
			Passed: false,
			Score:  0,
			Reason: "judge LLM returned empty response",
		}, nil
	}

	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	result, parseErr := parseJudgeResult(raw)
	if parseErr != nil {
		return &JudgeResult{
			Passed: false,
			Score:  0,
			Reason: raw,
		}, nil
	}

	result.Score = clampScore(result.Score)
	return result, nil
}

// buildJudgePrompt 组装 judge 使用的中文 prompt。prompt 指示模型只输出要求的 JSON 对象。
func buildJudgePrompt(req JudgeRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", req.Goal)
	fmt.Fprintf(&b, "Rubric: %s\n", req.Rubric)
	fmt.Fprintf(&b, "User Input: %s\n", req.UserInput)
	fmt.Fprintf(&b, "Agent Final Answer: %s\n", req.FinalAnswer)
	b.WriteString("Tool Outputs:\n")
	if len(req.ToolOutputs) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, out := range req.ToolOutputs {
			fmt.Fprintf(&b, "- %s\n", out)
		}
	}
	b.WriteString("\n")
	b.WriteString("请判断 Agent 的输出是否符合 Goal。返回 JSON，不要输出其他内容：\n")
	b.WriteString(`{"passed": true/false, "score": 0.0-1.0, "reason": "..."}`)
	return b.String()
}

// jsonBlockPrefix 匹配 markdown JSON 代码围栏的开始行。
var jsonBlockPrefix = regexp.MustCompile("(?i)^\\s*```(?:json)?\\s*")

// jsonBlockSuffix 匹配 markdown JSON 代码围栏的结束行。
var jsonBlockSuffix = regexp.MustCompile("(?i)^\\s*```\\s*$")

// parseJudgeResult 从原始 LLM 输出中提取 {"passed", "score", "reason"}。
// 它在 unmarshal 前剥离可选的 markdown 代码围栏。
func parseJudgeResult(raw string) (*JudgeResult, error) {
	lines := strings.Split(raw, "\n")
	var body strings.Builder
	inBlock := false
	for _, line := range lines {
		if !inBlock {
			if jsonBlockPrefix.MatchString(line) {
				inBlock = true
				continue
			}
			body.WriteString(line)
			body.WriteString("\n")
		} else {
			if jsonBlockSuffix.MatchString(line) {
				inBlock = false
				continue
			}
			body.WriteString(line)
			body.WriteString("\n")
		}
	}

	clean := strings.TrimSpace(body.String())
	var result JudgeResult
	if err := json.Unmarshal([]byte(clean), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// clampScore 将分数强制到 [0, 1]。
func clampScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
