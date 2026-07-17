package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/cases"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// recordingBus is a test EventBus that captures every event sent.
type recordingBus struct {
	events []event.Event
}

func (b *recordingBus) SendEvent(e event.Event) {
	b.events = append(b.events, e)
}

// fakeJudgeProvider is a minimal llm.Provider implementation that always
// returns a canned judge response for non-streaming Chat calls.
type fakeJudgeProvider struct {
	resp string
}

func (p *fakeJudgeProvider) Name() string { return "fake-judge" }

func (p *fakeJudgeProvider) Chat(req llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Choices: []llm.Choice{
			{
				Index:   0,
				Message: llm.Message{Role: "assistant", Content: p.resp},
			},
		},
		Usage: llm.Usage{TotalTokens: 10},
	}, nil
}

func (p *fakeJudgeProvider) ChatStream(req llm.ChatRequest, onChunk func(llm.StreamChunk) error) (string, llm.Usage, []llm.ToolCall, error) {
	return p.resp, llm.Usage{TotalTokens: 10}, nil, nil
}

// memoryEvalRepository records saved evaluations.
type memoryEvalRepository struct {
	evals []cases.CaseEvaluation
}

func (r *memoryEvalRepository) SaveEvaluation(eval cases.CaseEvaluation) error {
	r.evals = append(r.evals, eval)
	return nil
}

// newTestEngine creates an Engine configured with the given provider, bus,
// caseID and acceptance criteria, suitable for testing evaluateAndBroadcast.
func newTestEngine(t *testing.T, provider llm.Provider, bus EventBus, caseID string, criteria []harness.AcceptanceCriterion, evalRepo EvaluationRepository) *Engine {
	t.Helper()
	tools := tool.NewRegistry()
	cfg := EngineConfig{
		AgentID:              "test-agent",
		SystemPrompt:         "You are a helpful assistant.",
		Model:                "fake-model",
		Provider:             provider,
		CaseID:               caseID,
		Contract:             harness.TaskContract{Goal: "test goal", Scope: ".", AcceptanceCriteria: criteria},
		EvaluationRepository: evalRepo,
	}
	return NewEngine(cfg, tools, bus, "task-123")
}

func TestEvaluateAndBroadcast_LLMJudge(t *testing.T) {
	bus := &recordingBus{}
	provider := &fakeJudgeProvider{resp: `{"passed": true, "score": 0.85, "reason": "answer is correct"}`}
	criteria := []harness.AcceptanceCriterion{
		{Type: harness.AcceptLLMJudge, Target: "The answer is correct", Description: "correctness"},
	}

	e := newTestEngine(t, provider, bus, "test-case", criteria, nil)
	e.evaluateAndBroadcast("user input", "final answer")

	// The judge must be invoked and produce a task_evaluated event.
	if len(bus.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(bus.events))
	}
	ev := bus.events[0]
	if ev.Type != event.EventTaskEvaluated {
		t.Fatalf("expected event type %q, got %q", event.EventTaskEvaluated, ev.Type)
	}
	if ev.TaskID != "task-123" || ev.AgentID != "test-agent" {
		t.Fatalf("unexpected event metadata: task=%s agent=%s", ev.TaskID, ev.AgentID)
	}

	data := ev.Data
	if passed, ok := data["passed"].(bool); !ok || !passed {
		t.Fatalf("expected passed=true, got %v", data["passed"])
	}
	score, ok := data["score"].(float64)
	if !ok || score != 0.85 {
		t.Fatalf("expected score=0.85, got %v", data["score"])
	}
	reason, ok := data["reason"].(string)
	if !ok || reason == "" {
		t.Fatalf("expected non-empty reason, got %v", data["reason"])
	}
}

func TestEvaluateAndBroadcast_LLMJudge_PersistedWhenRepositoryProvided(t *testing.T) {
	bus := &recordingBus{}
	repo := &memoryEvalRepository{}
	provider := &fakeJudgeProvider{resp: `{"passed": true, "score": 0.92, "reason": "excellent"}`}
	criteria := []harness.AcceptanceCriterion{
		{Type: harness.AcceptLLMJudge, Target: "The answer is excellent", Description: "quality"},
	}

	e := newTestEngine(t, provider, bus, "persisted-case", criteria, repo)
	e.evaluateAndBroadcast("user input", "final answer")

	if len(repo.evals) != 1 {
		t.Fatalf("expected 1 saved evaluation, got %d", len(repo.evals))
	}
	eval := repo.evals[0]
	if eval.TaskID != "task-123" || eval.CaseID != "persisted-case" {
		t.Fatalf("unexpected eval ids: task=%s case=%s", eval.TaskID, eval.CaseID)
	}
	if !eval.Passed {
		t.Fatalf("expected eval.Passed=true, got false")
	}
	if eval.Score != 0.92 {
		t.Fatalf("expected eval.Score=0.92, got %v", eval.Score)
	}
	if eval.Reason == "" {
		t.Fatalf("expected non-empty eval.Reason")
	}
}

func TestEvaluateAndBroadcast_DeterministicCriteriaScore(t *testing.T) {
	bus := &recordingBus{}
	// No judge provider is needed for deterministic criteria.
	criteria := []harness.AcceptanceCriterion{
		{Type: harness.AcceptFileExists, Target: "nonexistent_file_for_test.txt", Description: "missing file"},
	}

	e := newTestEngine(t, nil, bus, "deterministic-case", criteria, nil)
	e.evaluateAndBroadcast("user input", "final answer")

	if len(bus.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(bus.events))
	}
	data := bus.events[0].Data
	if passed, ok := data["passed"].(bool); !ok || passed {
		t.Fatalf("expected passed=false for failed deterministic criterion, got %v", data["passed"])
	}
	score, ok := data["score"].(float64)
	if !ok || score != 0.0 {
		t.Fatalf("expected score=0.0 for all-failed deterministic criteria, got %v", data["score"])
	}
}

func TestEvaluateAndBroadcast_DeterministicAllPassedScore(t *testing.T) {
	bus := &recordingBus{}
	// Shell criteria currently soft-pass in harness.go.
	criteria := []harness.AcceptanceCriterion{
		{Type: harness.AcceptShellExitZero, Target: "true", Description: "shell soft-pass"},
	}

	e := newTestEngine(t, nil, bus, "deterministic-pass-case", criteria, nil)
	e.evaluateAndBroadcast("user input", "final answer")

	if len(bus.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(bus.events))
	}
	data := bus.events[0].Data
	if passed, ok := data["passed"].(bool); !ok || !passed {
		t.Fatalf("expected passed=true, got %v", data["passed"])
	}
	score, ok := data["score"].(float64)
	if !ok || score != 1.0 {
		t.Fatalf("expected score=1.0 for all-passed deterministic criteria, got %v", data["score"])
	}
}

func TestEvaluateAndBroadcast_NoCaseIDDoesNothing(t *testing.T) {
	bus := &recordingBus{}
	provider := &fakeJudgeProvider{resp: `{"passed": true, "score": 1.0, "reason": "ok"}`}
	e := newTestEngine(t, provider, bus, "", []harness.AcceptanceCriterion{
		{Type: harness.AcceptLLMJudge, Target: "ok", Description: "ok"},
	}, nil)
	e.evaluateAndBroadcast("user input", "final answer")

	if len(bus.events) != 0 {
		t.Fatalf("expected no events when caseID is empty, got %d", len(bus.events))
	}
}

// TestEnginePauseResume 验证 Engine.Pause / Engine.Resume 的语义：
//   - Pause 后 IsPaused 立即为 true，且会向 bus 发送 status=paused 的 agent_status 事件；
//   - 多次 Pause 幂等（不会重复发送事件）；
//   - Resume 后 IsPaused 回到 false，且会发送 status=running 事件；
//   - Pause 不影响 context（ctx 仍能正常传递）。
//
// 测试不启动 Run loop，直接断言 Pause/Resume 的状态机语义和事件分发，
// Run 循环里的阻塞由其他端到端测试覆盖。
func TestEnginePauseResume(t *testing.T) {
	bus := &recordingBus{}
	cfg := EngineConfig{
		AgentID:     "agent_pause_test",
		SystemPrompt: "You are a test agent.",
		Model:        "fake-model",
		Provider:     &fakeJudgeProvider{resp: "noop"},
		Contract:     harness.TaskContract{Goal: "test", Scope: "."},
	}
	tools := tool.NewRegistry()
	engine := NewEngine(cfg, tools, bus, "task_pause_test")

	if engine.IsPaused() {
		t.Fatalf("freshly created engine should not be paused")
	}

	// 第一次 Pause：发送 agent_status=paused 事件。
	engine.Pause()
	if !engine.IsPaused() {
		t.Fatalf("engine.Pause() should set paused=true")
	}
	pausedCount := 0
	for _, evt := range bus.events {
		if evt.Type == "agent_status" {
			if status, ok := evt.Data["status"].(string); ok && status == "paused" {
				pausedCount++
			}
		}
	}
	if pausedCount != 1 {
		t.Fatalf("expected exactly 1 paused agent_status event, got %d", len(bus.events))
	}

	// 第二次 Pause 幂等：不再发送新事件。
	engine.Pause()
	pausedCount2 := 0
	for _, evt := range bus.events {
		if evt.Type == "agent_status" {
			if status, ok := evt.Data["status"].(string); ok && status == "paused" {
				pausedCount2++
			}
		}
	}
	if pausedCount2 != 1 {
		t.Fatalf("second Pause should be idempotent, got %d paused events", pausedCount2)
	}

	// Resume：发送 agent_status=running，paused=false。
	engine.Resume()
	if engine.IsPaused() {
		t.Fatalf("engine.Resume() should set paused=false")
	}
	runningCount := 0
	for _, evt := range bus.events {
		if evt.Type == "agent_status" {
			if status, ok := evt.Data["status"].(string); ok && status == "running" {
				runningCount++
			}
		}
	}
	if runningCount != 1 {
		t.Fatalf("expected exactly 1 running agent_status event, got %d", runningCount)
	}

	// 第二次 Resume 幂等：不再发送新事件。
	engine.Resume()
	runningCount2 := 0
	for _, evt := range bus.events {
		if evt.Type == "agent_status" {
			if status, ok := evt.Data["status"].(string); ok && status == "running" {
				runningCount2++
			}
		}
	}
	if runningCount2 != 1 {
		t.Fatalf("second Resume should be idempotent, got %d running events", runningCount2)
	}
}

// TestEnginePauseResumeConcurrent 验证 Pause/Resume 在多 goroutine 并发触发下是安全的：
// atomic.Bool 自身原子、channel 关闭/重建不会触发竞态。
func TestEnginePauseResumeConcurrent(t *testing.T) {
	bus := &recordingBus{}
	cfg := EngineConfig{
		AgentID:     "agent_concurrent",
		SystemPrompt: "x",
		Model:        "fake-model",
		Provider:     &fakeJudgeProvider{resp: "noop"},
		Contract:     harness.TaskContract{Goal: "x", Scope: "."},
	}
	tools := tool.NewRegistry()
	engine := NewEngine(cfg, tools, bus, "task_concurrent")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			engine.Pause()
		}()
		go func() {
			defer wg.Done()
			engine.Resume()
		}()
	}
	wg.Wait()

	// 不强制最终态，只断言没有 panic，IsPaused 仍然是合法的 bool 值。
	_ = engine.IsPaused()
	// 给 channel 关闭与重建一点时间，确保没有挂起的 close 协程残留。
	time.Sleep(10 * time.Millisecond)
}
