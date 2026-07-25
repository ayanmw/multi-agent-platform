package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// recordingBusWithFilter 是支持按事件类型捕获的 recordingBus 扩展。
// P3-5 中 Engine 路由事件测试需要精确断言 model_routed / model_fallback_used /
// cost_budget_exceeded 三种事件的发出。
type recordingBusWithFilter struct {
	mu     sync.Mutex
	events []event.Event
}

func (b *recordingBusWithFilter) SendEvent(e event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
}

func (b *recordingBusWithFilter) eventsOfType(t string) []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []event.Event
	for _, e := range b.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

func (b *recordingBusWithFilter) hasEvent(t string) bool {
	return len(b.eventsOfType(t)) > 0
}

// routingFakeProvider 是一个支持控制失败/成功的 Provider，用于验证 fallback 路径。
// 这里同时实现 Chat（给 Router 的 classifier 用）和 ChatStream（给 Engine think 用）。
// 通过 failStream 控制 ChatStream 是否失败，从而精确触发模型 ChatStream fallback。
type routingFakeProvider struct {
	name       string
	failStream bool
	resp       string
}

func (p *routingFakeProvider) Name() string { return p.name }

func (p *routingFakeProvider) Chat(req llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Choices: []llm.Choice{
			{Index: 0, Message: llm.Message{Role: "assistant", Content: p.resp}},
		},
		Usage: llm.Usage{TotalTokens: 5},
	}, nil
}

func (p *routingFakeProvider) ChatStream(req llm.ChatRequest, onChunk func(llm.StreamChunk) error) (string, llm.Usage, []llm.ToolCall, error) {
	if p.failStream {
		return "", llm.Usage{}, nil, errors.New("simulated provider failure")
	}
	return p.resp, llm.Usage{TotalTokens: 10}, nil, nil
}

func (p *routingFakeProvider) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{}, nil
}

// newFakeRegistry 返回一个只含指定模型的 registry，避免被 DefaultProfiles 中
// 大量模型和 fallback 链干扰，使测试对路由结果可预测。
func newFakeRegistry(profiles []*llm.ModelProfile) *llm.ModelRegistry {
	registry := llm.NewModelRegistry()
	for _, p := range profiles {
		registry.Register(p)
	}
	return registry
}

func TestEngine_Routing_ModelRoutedEvent(t *testing.T) {
	bus := &recordingBusWithFilter{}

	classifier := &routingFakeProvider{
		name: "fake-classifier",
		resp: `{"primary_intent":"simple_chat","confidence":0.95,"estimated_steps":1}`,
	}
	primaryProvider := &routingFakeProvider{
		name: "fake-primary",
		resp: "hello from primary",
	}

	registry := newFakeRegistry([]*llm.ModelProfile{
		{
			Name:             "primary-model",
			Provider:         "fake-primary",
			Tier:             llm.TierEfficient,
			Capabilities:     []llm.ModelCapability{llm.CapToolCalling, llm.CapStreaming},
			InputPrice:       0.1,
			OutputPrice:      0.2,
			MaxContextWindow: 128000,
			FallbackModel:    "",
		},
	})

	router := llm.NewRouter(registry, classifier, nil)
	router.SetBroadcaster(&engineRoutingEventBroadcaster{bus}, "task-routed", "test-agent")

	cfg := EngineConfig{
		AgentID:      "test-agent",
		SystemPrompt: "You are a router test agent.",
		Model:        "primary-model",
		Provider:     primaryProvider,
		Router:       router,
		Registry:     registry,
		Providers: map[string]llm.Provider{
			"primary-model": primaryProvider,
		},
	}

	tools := tool.NewRegistry()
	engine := NewEngine(cfg, tools, bus, "task-routed")

	_, _, err := engine.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	if !bus.hasEvent(event.EventModelRouted) {
		t.Fatalf("expected %s event", event.EventModelRouted)
	}
	if bus.hasEvent(event.EventModelFallbackUsed) {
		t.Fatalf("did not expect fallback event for successful primary")
	}
}

func TestEngine_Routing_ModelFallbackUsedEvent(t *testing.T) {
	bus := &recordingBusWithFilter{}

	classifier := &routingFakeProvider{
		name: "fake-classifier",
		resp: `{"primary_intent":"code_generation","confidence":0.95,"estimated_steps":2}`,
	}
	primaryProvider := &routingFakeProvider{
		name:       "fake-primary",
		resp:       "should fail",
		failStream: true,
	}
	fallbackProvider := &routingFakeProvider{
		name: "fake-fallback",
		resp: "hello from fallback",
	}

	registry := newFakeRegistry([]*llm.ModelProfile{
		{
			Name:             "primary-model",
			Provider:         "fake-primary",
			Tier:             llm.TierStandard,
			Capabilities:     []llm.ModelCapability{llm.CapToolCalling, llm.CapStreaming},
			InputPrice:       0.5,
			OutputPrice:      1.0,
			MaxContextWindow: 128000,
			FallbackModel:    "fallback-model",
		},
		{
			Name:             "fallback-model",
			Provider:         "fake-fallback",
			Tier:             llm.TierEfficient,
			Capabilities:     []llm.ModelCapability{llm.CapToolCalling, llm.CapStreaming},
			InputPrice:       0.1,
			OutputPrice:      0.2,
			MaxContextWindow: 128000,
			FallbackModel:    "",
		},
	})

	router := llm.NewRouter(registry, classifier, nil)
	router.SetBroadcaster(&engineRoutingEventBroadcaster{bus}, "task-fallback", "test-agent")

	cfg := EngineConfig{
		AgentID:      "test-agent",
		SystemPrompt: "You are a fallback test agent.",
		Model:        "primary-model",
		Provider:     primaryProvider,
		Router:       router,
		Registry:     registry,
		Providers: map[string]llm.Provider{
			"primary-model":  primaryProvider,
			"fallback-model": fallbackProvider,
		},
	}

	tools := tool.NewRegistry()
	engine := NewEngine(cfg, tools, bus, "task-fallback")

	_, _, err := engine.Run(context.Background(), "write a fibonacci function")
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	if !bus.hasEvent(event.EventModelRouted) {
		t.Fatalf("expected %s event", event.EventModelRouted)
	}
	fallbacks := bus.eventsOfType(event.EventModelFallbackUsed)
	if len(fallbacks) != 1 {
		t.Fatalf("expected exactly one %s event, got %d", event.EventModelFallbackUsed, len(fallbacks))
	}
	data := fallbacks[0].Data
	if data["primary"] == "" || data["fallback"] == "" {
		t.Fatalf("fallback event missing primary/fallback data: %+v", data)
	}
}

// TestEngine_Routing_CostBudgetExceeded 验证当 Agent 级 MaxCostUSD 预算
// 被触发时，Engine 会返回错误并广播 cost_budget_exceeded 事件。
// 测试要点：预算拦截发生在 Router 选择模型之后、真实 LLM 调用之前。
// 若 Router 因 BudgetUSD 过滤把所有候选都排除，则无法进入预算拦截分支，
// 因此本测试构造一个满足 Router 预算过滤但会让 Engine 输入估算超支的场景：
// InputPrice 低于 MaxCostUSD（per-1M-token 单价可通过路由过滤），但上下文
// 估算 token 数足够大，使 contextLen*InputPrice/1M > MaxCostUSD。这样
// Router.Select 能成功返回模型，Engine 的预算拦截才会生效。
func TestEngine_Routing_CostBudgetExceeded(t *testing.T) {
	bus := &recordingBusWithFilter{}

	classifier := &routingFakeProvider{
		name: "fake-classifier",
		resp: `{"primary_intent":"complex_reasoning","confidence":0.95,"estimated_steps":5}`,
	}
	provider := &routingFakeProvider{
		name: "fake-primary",
		resp: "hello",
	}

	// 模型单价 0.5 USD/1M tokens，小于 MaxCostUSD=0.000001，因此 Router
	// 的 BudgetUSD 过滤不会排除它。但 100000 字符 ≈ 25000 tokens，乘以 0.5/1M
	// 得到 0.0125 USD，远超 0.000001 USD，能触发 Engine 的预算拦截。
	registry := newFakeRegistry([]*llm.ModelProfile{
		{
			Name:             "expensive-model",
			Provider:         "fake-primary",
			Tier:             llm.TierPremium,
			Capabilities:     []llm.ModelCapability{llm.CapToolCalling, llm.CapStreaming},
			InputPrice:       0.5,
			OutputPrice:      1.0,
			MaxContextWindow: 200000,
			FallbackModel:    "",
		},
	})

	router := llm.NewRouter(registry, classifier, nil)
	router.SetBroadcaster(&engineRoutingEventBroadcaster{bus}, "task-budget", "test-agent")

	longContent := make([]byte, 100000)
	cfg := EngineConfig{
		AgentID:      "test-agent",
		SystemPrompt: "You are a budget test agent.",
		Model:        "expensive-model",
		Provider:     provider,
		Router:       router,
		Registry:     registry,
		Providers: map[string]llm.Provider{
			"expensive-model": provider,
		},
		MaxCostUSD: 0.000001,
		MaxSteps:   2,
	}

	tools := tool.NewRegistry()
	engine := NewEngine(cfg, tools, bus, "task-budget")
	engine.appendMessage(llm.Message{Role: "user", Content: string(longContent)})

	_, _, err := engine.Run(context.Background(), "solve an architecture problem with a very long prompt budget exceeded scenario")
	if err == nil {
		t.Fatalf("expected error due to cost budget exceeded")
	}
	if !contains(err.Error(), "cost budget") && !contains(err.Error(), "budget") {
		t.Fatalf("expected cost budget error, got: %v", err)
	}

	budgetEvents := bus.eventsOfType(event.EventCostBudgetExceeded)
	if len(budgetEvents) != 1 {
		t.Fatalf("expected exactly one %s event, got %d", event.EventCostBudgetExceeded, len(budgetEvents))
	}
	data := budgetEvents[0].Data
	if data["current_cost_usd"] == nil || data["max_cost_usd"] == nil {
		t.Fatalf("budget event missing current/max cost data: %+v", data)
	}
}

// engineRoutingEventBroadcaster 把 Router 的 llm.EventBroadcaster 适配为 runtime.EventBus。
// Router 在 intent_classified 等事件中使用 llm.EventBroadcaster，需要转换成 event.Event
// 才能被 recordingBusWithFilter 捕获。
type engineRoutingEventBroadcaster struct {
	bus *recordingBusWithFilter
}

func (a *engineRoutingEventBroadcaster) SendEvent(eventType string, data map[string]any) {
	a.bus.SendEvent(event.NewEvent(eventType, "task-routing", "test-agent", 0, data))
}
