package llm

import (
	"context"
	"errors"
	"testing"
)

// fakeBroadcaster 是用于 Router 事件测试的内存广播器。
type fakeBroadcaster struct {
	events []fakeBroadcastEvent
}

type fakeBroadcastEvent struct {
	EventType string
	Data      map[string]any
}

func (f *fakeBroadcaster) SendEvent(eventType string, data map[string]any) {
	f.events = append(f.events, fakeBroadcastEvent{EventType: eventType, Data: data})
}

// fakeProviderForRouterEvents 是仅返回固定分类 JSON 的 provider。
type fakeProviderForRouterEvents struct {
	response *ChatResponse
}

func (p *fakeProviderForRouterEvents) Chat(req ChatRequest) (*ChatResponse, error) {
	if p.response != nil {
		return p.response, nil
	}
	return &ChatResponse{
		Choices: []Choice{{
			Message: Message{
				Content: `{"primary_intent":"code_generation","secondary_intents":[],"confidence":0.9,"needs_tools":["write_file"],"estimated_steps":2}`,
			},
		}},
	}, nil
}

func (p *fakeProviderForRouterEvents) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	return "", Usage{}, nil, errors.New("not implemented")
}

func (p *fakeProviderForRouterEvents) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return []ModelInfo{}, nil
}

func (p *fakeProviderForRouterEvents) Name() string { return "fake-router-classifier" }

// TestRouter_EmitIntentClassified 验证 Select 成功时会广播 intent_classified 事件。
func TestRouter_EmitIntentClassified(t *testing.T) {
	reg := NewModelRegistry()
	for _, p := range DefaultProfiles() {
		reg.Register(p)
	}

	classifier := &fakeProviderForRouterEvents{}
	router := NewRouter(reg, classifier, nil)
	bus := &fakeBroadcaster{}
	router.SetBroadcaster(bus, "task-1", "agent-1")

	_, err := router.Select(context.Background(), &RouteRequest{
		UserInput:    "write a function",
		RequiredCaps: []ModelCapability{CapToolCalling},
	})
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}

	found := false
	for _, e := range bus.events {
		if e.EventType == "intent_classified" {
			found = true
			if e.Data["primary_intent"] != "code_generation" {
				t.Fatalf("expected primary_intent=code_generation, got %v", e.Data["primary_intent"])
			}
		}
	}
	if !found {
		t.Fatalf("expected intent_classified event, got %v", bus.events)
	}
}

// TestRouter_EmitModelRateLimited 验证候选模型被限流时广播 model_rate_limited 事件。
func TestRouter_EmitModelRateLimited(t *testing.T) {
	reg := NewModelRegistry()
	for _, p := range DefaultProfiles() {
		reg.Register(p)
	}

	// 构造一个总返回 simple_chat 的分类器，使目标层级为 TierEfficient。
	classifier := &fakeProviderForRouterEvents{
		response: &ChatResponse{
			Choices: []Choice{{
				Message: Message{
					Content: `{"primary_intent":"simple_chat","secondary_intents":[],"confidence":0.9,"needs_tools":[],"estimated_steps":1}`,
				},
			}},
		},
	}

	lim := NewRateLimiter()
	// 把 TierEfficient、TierFree、TierLightweight 层所有模型都限流：RPM=1 并做 2 次调用，
	// 使 recent(2) > limit(1)，IsLimitExceeded 返回 true。
	for _, tier := range []ModelTier{TierFree, TierEfficient, TierLightweight} {
		for _, m := range reg.GetByTier(tier) {
			lim.SetLimit(m.Name, 1)
			lim.RecordCall(m.Name)
			lim.RecordCall(m.Name)
		}
	}

	router := NewRouter(reg, classifier, lim)
	bus := &fakeBroadcaster{}
	router.SetBroadcaster(bus, "task-2", "agent-2")

	decision, err := router.Select(context.Background(), &RouteRequest{UserInput: "hello"})
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}

	var rateLimitedCount int
	for _, e := range bus.events {
		if e.EventType == "model_rate_limited" {
			rateLimitedCount++
		}
	}
	if rateLimitedCount == 0 {
		t.Fatalf("expected model_rate_limited events, got %v; decision=%+v", bus.events, decision)
	}
}
