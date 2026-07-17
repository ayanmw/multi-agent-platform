package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// LLMDecomposer 使用 LLM 把用户请求动态分解为多个 AgentSpec。
//
// 当没有配置 provider 或处于 mock 模式时，它会退化为规则分解器，
// 保证测试环境和 LLM 不可用时系统仍能正常启动多 agent 任务。
// 解析失败同样会回退到规则分解，避免因为 LLM 输出格式问题导致任务直接失败。
type LLMDecomposer struct {
	cfg      *config.Config
	provider llm.Provider
}

// NewLLMDecomposer 创建一个新的 LLM 分解器。
func NewLLMDecomposer(cfg *config.Config, provider llm.Provider) *LLMDecomposer {
	return &LLMDecomposer{cfg: cfg, provider: provider}
}

// Decompose 根据用户输入和期望策略返回分解后的 agent 规范。
func (d *LLMDecomposer) Decompose(input, requestedStrategy string) (*DecomposeResult, error) {
	// 无 provider 或 mock 模式：回退规则分解，保证可测试性。
	if d.provider == nil || (d.cfg != nil && d.cfg.LLMUseMock) {
		return (&TaskDecomposer{}).Decompose(input, requestedStrategy)
	}

	model := ""
	if d.cfg != nil {
		model = d.cfg.LLMModel
	}

	prompt := buildDecompositionPrompt(input, requestedStrategy)
	resp, err := d.provider.Chat(llm.ChatRequest{
		Model:       model,
		Messages:    []llm.Message{{Role: "system", Content: "You are a task decomposition engine. Output only valid JSON."}, {Role: "user", Content: prompt}},
		Temperature: 0,
	})
	if err != nil {
		log.Printf("[LLMDecomposer] LLM call failed, falling back: %v", err)
		return (&TaskDecomposer{}).Decompose(input, requestedStrategy)
	}
	if resp == nil || len(resp.Choices) == 0 {
		return (&TaskDecomposer{}).Decompose(input, requestedStrategy)
	}

	result, parseErr := parseDecomposeResponse(resp.Choices[0].Message.Content, input, requestedStrategy)
	if parseErr != nil {
		log.Printf("[LLMDecomposer] parse failed, falling back: %v", parseErr)
		return (&TaskDecomposer{}).Decompose(input, requestedStrategy)
	}
	return result, nil
}

func buildDecompositionPrompt(input, strategy string) string {
	return fmt.Sprintf(`Given the user request, break it into agent roles.
Output strictly JSON with fields:
- strategy: one of parallel/sequential/pipeline
- agents: array of {agent_id, name, system_prompt, input, allowed_tools, output_to, model}
Preferred strategy: %s
Input: %s`, strategy, input)
}

func parseDecomposeResponse(content, originalInput, requestedStrategy string) (*DecomposeResult, error) {
	content = strings.TrimSpace(content)
	if idx := strings.Index(content, "{"); idx >= 0 {
		content = content[idx:]
	}
	if idx := strings.LastIndex(content, "}"); idx >= 0 {
		content = content[:idx+1]
	}

	var payload struct {
		Strategy string `json:"strategy"`
		Agents   []struct {
			AgentID      string   `json:"agent_id"`
			Name         string   `json:"name"`
			SystemPrompt string   `json:"system_prompt"`
			Input        string   `json:"input"`
			AllowedTools []string `json:"allowed_tools"`
			OutputTo     []string `json:"output_to"`
			Model        string   `json:"model"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil, err
	}

	if payload.Strategy == "" {
		payload.Strategy = requestedStrategy
	}
	if payload.Strategy == "" {
		payload.Strategy = "parallel"
	}

	specs := make([]AgentSpec, len(payload.Agents))
	for i, a := range payload.Agents {
		if a.AgentID == "" {
			a.AgentID = fmt.Sprintf("agent_%d", i+1)
		}
		if a.Name == "" {
			a.Name = a.AgentID
		}
		if a.Input == "" {
			a.Input = originalInput
		}
		specs[i] = AgentSpec{
			AgentID:      a.AgentID,
			Name:         a.Name,
			SystemPrompt: a.SystemPrompt,
			Input:        a.Input,
			AllowedTools: a.AllowedTools,
			OutputTo:     a.OutputTo,
			Model:        a.Model,
		}
	}
	return &DecomposeResult{Agents: specs, Strategy: payload.Strategy}, nil
}

// NewTaskDecomposer 创建规则分解器。
func NewTaskDecomposer() *TaskDecomposer { return &TaskDecomposer{} }

// Decomposer 是任务分解器的统一接口。
type Decomposer interface {
	Decompose(input, requestedStrategy string) (*DecomposeResult, error)
}
