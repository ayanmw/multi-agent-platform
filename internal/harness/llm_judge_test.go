package harness

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// fakeJudgeProvider 实现了 judgeChatClient（与 llm.Provider），返回脚本化的响应。
type fakeJudgeProvider struct {
	respContent string
	respErr     error
	chatHook    func(req llm.ChatRequest)
}

func (f *fakeJudgeProvider) Name() string { return "fake-judge" }

func (f *fakeJudgeProvider) Chat(req llm.ChatRequest) (*llm.ChatResponse, error) {
	if f.chatHook != nil {
		f.chatHook(req)
	}
	if f.respErr != nil {
		return nil, f.respErr
	}
	return &llm.ChatResponse{
		Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: f.respContent}}},
	}, nil
}

func (f *fakeJudgeProvider) ChatStream(req llm.ChatRequest, onChunk func(llm.StreamChunk) error) (string, llm.Usage, []llm.ToolCall, error) {
	return "", llm.Usage{}, nil, errors.New("not implemented")
}

// extractJudgeRequest 从 chat message prompt 中提取 JudgeRequest 字段，便于测试验证
// context 传递。
func extractJudgeRequest(req llm.ChatRequest) JudgeRequest {
	var r JudgeRequest
	if len(req.Messages) == 0 {
		return r
	}
	content := req.Messages[0].Content
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "Goal: ") {
			r.Goal = strings.TrimPrefix(line, "Goal: ")
		} else if strings.HasPrefix(line, "Rubric: ") {
			r.Rubric = strings.TrimPrefix(line, "Rubric: ")
		} else if strings.HasPrefix(line, "User Input: ") {
			r.UserInput = strings.TrimPrefix(line, "User Input: ")
		} else if strings.HasPrefix(line, "Agent Final Answer: ") {
			r.FinalAnswer = strings.TrimPrefix(line, "Agent Final Answer: ")
		} else if strings.HasPrefix(line, "- ") {
			r.ToolOutputs = append(r.ToolOutputs, strings.TrimPrefix(line, "- "))
		}
	}
	return r
}

func TestParseJudgeResultValid(t *testing.T) {
	raw := `{"passed": true, "score": 0.85, "reason": "Good answer"}`
	res, err := parseJudgeResult(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !res.Passed || res.Score != 0.85 || res.Reason != "Good answer" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestParseJudgeResultWithMarkdownFence(t *testing.T) {
	raw := "```json\n{\"passed\": false, \"score\": 0.2, \"reason\": \"_missing details_\"}\n```"
	res, err := parseJudgeResult(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Passed || res.Score != 0.2 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestParseJudgeResultInvalidReturnsError(t *testing.T) {
	_, err := parseJudgeResult("not json")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClampScore(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{-0.5, 0},
		{0, 0},
		{0.5, 0.5},
		{1, 1},
		{1.5, 1},
	}
	for _, c := range cases {
		got := clampScore(c.in)
		if got != c.want {
			t.Errorf("clampScore(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestLLMJudgeEvaluate(t *testing.T) {
	provider := &fakeJudgeProvider{respContent: `{"passed": true, "score": 0.95, "reason": "ok"}`}
	judge := NewLLMJudge(provider, "judge-model")
	res, err := judge.Evaluate(context.Background(), JudgeRequest{
		Goal:        "test",
		Rubric:      "does it say hello",
		UserInput:   "say hello",
		FinalAnswer: "hello world",
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !res.Passed || res.Score != 0.95 || res.Reason != "ok" {
		t.Fatalf("unexpected: %+v", res)
	}
}

func TestLLMJudgeEvaluateMalformedReturnsRawReason(t *testing.T) {
	provider := &fakeJudgeProvider{respContent: "I think it passes"}
	judge := NewLLMJudge(provider, "judge-model")
	res, err := judge.Evaluate(context.Background(), JudgeRequest{})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if res.Passed || res.Score != 0 {
		t.Fatalf("expected failed zero score, got %+v", res)
	}
	if res.Reason != "I think it passes" {
		t.Fatalf("expected raw response as reason, got %q", res.Reason)
	}
}

func TestLLMJudgeEvaluateProviderError(t *testing.T) {
	provider := &fakeJudgeProvider{respErr: errors.New("network error")}
	judge := NewLLMJudge(provider, "judge-model")
	_, err := judge.Evaluate(context.Background(), JudgeRequest{})
	if err == nil || !strings.Contains(err.Error(), "judge LLM call failed") {
		t.Fatalf("expected judge LLM call failed, got %v", err)
	}
}

func TestAcceptanceEvaluatorLLMJudge(t *testing.T) {
	provider := &fakeJudgeProvider{respContent: `{"passed": true, "score": 0.8, "reason": "rubric met"}`}
	ae := NewAcceptanceEvaluator(".")
	ae.SetLLMJudge(NewLLMJudge(provider, "m"))
	ae.SetEvaluationContext("greet user", "say hello", "hello world", []string{"tool output"})

	report, err := ae.Evaluate([]AcceptanceCriterion{
		{Type: AcceptLLMJudge, Target: "answer mentions hello", Description: "greeting check"},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !report.AllPassed {
		t.Fatalf("expected all passed, got %+v", report)
	}
	if len(report.Results) == 0 {
		t.Fatal("expected results")
	}
	msg := report.Results[0].Message
	if !strings.Contains(msg, "score=0.80") {
		t.Fatalf("expected score in message, got %q", msg)
	}
}

func TestAcceptanceEvaluatorLLMJudgeContextFlows(t *testing.T) {
	var capturedReq JudgeRequest
	provider := &fakeJudgeProvider{}
	provider.chatHook = func(req llm.ChatRequest) {
		capturedReq = extractJudgeRequest(req)
	}
	provider.respContent = `{"passed": true, "score": 1.0, "reason": "all good"}`

	ae := NewAcceptanceEvaluator(".")
	ae.SetLLMJudge(NewLLMJudge(provider, "m"))
	ae.SetEvaluationContext("goal-123", "input-456", "final-789", []string{"out-a", "out-b"})

	_, err := ae.Evaluate([]AcceptanceCriterion{
		{Type: AcceptLLMJudge, Target: "rubric-xyz", Description: "context check"},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	if capturedReq.Goal != "goal-123" || capturedReq.UserInput != "input-456" || capturedReq.FinalAnswer != "final-789" {
		t.Fatalf("judge request missing context: %+v", capturedReq)
	}
	if len(capturedReq.ToolOutputs) != 2 || capturedReq.ToolOutputs[0] != "out-a" || capturedReq.ToolOutputs[1] != "out-b" {
		t.Fatalf("judge tool outputs wrong: %+v", capturedReq.ToolOutputs)
	}
}

func TestAcceptanceEvaluatorLLMJudgeWithoutJudgeSoftPasses(t *testing.T) {
	ae := NewAcceptanceEvaluator(".")
	report, err := ae.Evaluate([]AcceptanceCriterion{
		{Type: AcceptLLMJudge, Target: "answer mentions hello", Description: "greeting check"},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !report.AllPassed {
		t.Fatalf("expected soft pass, got %+v", report)
	}
}
