// Package harness — LLM Judge: semantic evaluation of agent output against a rubric.
//
// Deterministic acceptance criteria (file_exists, content_contains, shell_exit_zero)
// are implemented in harness.go. LLMJudge complements them by letting a contract
// specify a free-text rubric that an LLM evaluates against the agent's final answer
// and recent tool outputs.
//
// # Design notes
//
//   - The judge uses a dedicated non-streaming chat call so it can be run once at
//     task completion without adding latency to the ReAct loop.
//   - The LLM is asked to return a strict JSON object; the parser tolerates common
//     wrapping (markdown fences, extra whitespace) and degrades gracefully when
//     the response is not valid JSON.
//   - Scores are clamped to [0, 1] regardless of what the model returns.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// JudgeRequest contains everything the judge needs to evaluate an agent run.
type JudgeRequest struct {
	// Goal is the high-level objective from the TaskContract.
	Goal string

	// Rubric is the criterion-specific question or scoring guide.
	Rubric string

	// UserInput is the original user request that started the task.
	UserInput string

	// FinalAnswer is the agent's final answer produced at task completion.
	FinalAnswer string

	// ToolOutputs are the last N significant tool results observed by the agent.
	// They provide evidence the judge can use but are optional.
	ToolOutputs []string
}

// JudgeResult is the structured output of an LLM judge evaluation.
type JudgeResult struct {
	Passed bool    `json:"passed"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// judgeChatClient is the minimal capability LLMJudge needs from the LLM layer.
// It matches llm.Provider.Chat, so any provider can be supplied without adding
// a dependency on streaming internals.
type judgeChatClient interface {
	Chat(req llm.ChatRequest) (*llm.ChatResponse, error)
}

// LLMJudge evaluates agent output against a rubric using an LLM.
//
// It intentionally depends on the small judgeChatClient interface rather than the
// full llm.Router so that unit tests can supply a stub and so the judge can be
// anchored to a specific model (typically a cheap/efficient model).
type LLMJudge struct {
	client judgeChatClient
	model  string
}

// NewLLMJudge creates a judge backed by the given provider and model.
//
// If model is empty, the provider's default model is used. The provider must
// satisfy llm.Provider; the constructor accepts llm.Provider directly.
func NewLLMJudge(provider llm.Provider, model string) *LLMJudge {
	return &LLMJudge{client: provider, model: model}
}

// Evaluate asks the LLM to judge whether the final answer satisfies the rubric.
//
// It returns Passed=false/Score=0 with the raw response as Reason when the model
// output cannot be parsed as JSON, so a malformed judge response never crashes
// the evaluator.
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

// buildJudgePrompt assembles the Chinese prompt used by the judge.
// The prompt instructs the model to output only the requested JSON object.
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

// jsonBlockPrefix matches a markdown JSON code fence start line.
var jsonBlockPrefix = regexp.MustCompile("(?i)^\\s*```(?:json)?\\s*")

// jsonBlockSuffix matches a markdown JSON code fence end line.
var jsonBlockSuffix = regexp.MustCompile("(?i)^\\s*```\\s*$")

// parseJudgeResult extracts {"passed", "score", "reason"} from raw LLM output.
// It strips optional markdown fences before unmarshalling.
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

// clampScore forces the score into [0, 1].
func clampScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
