package runtime

import (
	"sync"
	"testing"

	"github.com/ayanmw/multi-agent-platform/internal/harness"
	"github.com/ayanmw/multi-agent-platform/internal/tool"
)

// recordingAgentBus 是一个测试用 AgentBus，记录所有经 SendMessage 发出的消息，
// 以便断言路由目标（N0-01 回归）。
type recordingAgentBus struct {
	mu   sync.Mutex
	sent []AgentMessage
}

func (b *recordingAgentBus) RegisterHandler(agentID string, handler func(AgentMessage)) {}
func (b *recordingAgentBus) RegisterHandlerBySubTask(a, s string, h func(AgentMessage)) {}
func (b *recordingAgentBus) UnregisterHandler(agentID string)                           {}
func (b *recordingAgentBus) UnregisterHandlerBySubTask(agentID, subTaskID string)       {}

func (b *recordingAgentBus) SendMessage(msg AgentMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, msg)
}

func (b *recordingAgentBus) messages() []AgentMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]AgentMessage, len(b.sent))
	copy(out, b.sent)
	return out
}

// 编译期保证 recordingAgentBus 实现了 runtime.AgentBus。
var _ AgentBus = (*recordingAgentBus)(nil)

// newRoutingTestEngine 构造一个仅用于 AgentBus 路由断言的最小 Engine。
// 它不会调用 Run()，因此无需 Provider 真正产出内容。
func newRoutingTestEngine(t *testing.T, cfg EngineConfig) (*Engine, *recordingBus, *recordingAgentBus) {
	t.Helper()
	evBus := &recordingBus{}
	agBus := &recordingAgentBus{}
	cfg.AgentBus = agBus
	if cfg.Contract.Goal == "" {
		cfg.Contract = harness.TaskContract{Goal: "routing test", Scope: "."}
	}
	if cfg.Model == "" {
		cfg.Model = "fake-model"
	}
	if cfg.Provider == nil {
		cfg.Provider = &fakeJudgeProvider{resp: "ack"}
	}
	return NewEngine(cfg, tool.NewRegistry(), evBus, "task-routing"), evBus, agBus
}

// TestSupervisorAgentIDDefaultsToLeader 验证未显式配置 SupervisorAgentID 时，
// Engine 回退到 DefaultSupervisorAgentID（"leader"）；显式配置时以配置为准。
func TestSupervisorAgentIDDefaultsToLeader(t *testing.T) {
	engine, _, _ := newRoutingTestEngine(t, EngineConfig{
		AgentID:             "agent_writer",
		SubTaskID:           "root_agent_writer",
		SupervisorSubTaskID: "root",
		Role:                AgentRoleWorker,
	})
	if got := engine.supervisorAgentID(); got != DefaultSupervisorAgentID {
		t.Fatalf("默认 supervisor agent ID = %q，期望 %q", got, DefaultSupervisorAgentID)
	}

	custom, _, _ := newRoutingTestEngine(t, EngineConfig{
		AgentID:             "agent_writer",
		SubTaskID:           "root_agent_writer",
		SupervisorSubTaskID: "root",
		SupervisorAgentID:   "agent_manager",
		Role:                AgentRoleWorker,
	})
	if got := custom.supervisorAgentID(); got != "agent_manager" {
		t.Fatalf("显式 supervisor agent ID = %q，期望 %q", got, "agent_manager")
	}
}

// TestSendAgentMessageToRoutesToSupervisor 是 N0-01 的核心回归：
// worker 发往 supervisor 的消息必须带上真实的 ToAgentID 与 SubTaskID，
// 使 AgentBus 能精确匹配到 leader Engine 注册的 (agentID, subTaskID) handler。
func TestSendAgentMessageToRoutesToSupervisor(t *testing.T) {
	engine, evBus, agBus := newRoutingTestEngine(t, EngineConfig{
		AgentID:             "agent_writer",
		SubTaskID:           "root_agent_writer",
		SupervisorSubTaskID: "root",
		Role:                AgentRoleWorker,
	})

	ok := engine.sendAgentMessageTo(engine.supervisorAgentID(), engine.cfg.SupervisorSubTaskID, "approval_request", "需要审批")
	if !ok {
		t.Fatalf("sendAgentMessageTo 返回 false，期望投递成功")
	}

	msgs := agBus.messages()
	if len(msgs) != 1 {
		t.Fatalf("AgentBus 收到 %d 条消息，期望 1 条", len(msgs))
	}
	msg := msgs[0]
	if msg.ToAgentID != DefaultSupervisorAgentID {
		t.Errorf("ToAgentID = %q，期望 %q（空目标即 N0-01 的路由 bug）", msg.ToAgentID, DefaultSupervisorAgentID)
	}
	if msg.SubTaskID != "root" {
		t.Errorf("SubTaskID = %q，期望 %q", msg.SubTaskID, "root")
	}
	if msg.FromAgentID != "agent_writer" || msg.FromSubTaskID != "root_agent_writer" {
		t.Errorf("发送方身份错误: from=%q from_sub=%q", msg.FromAgentID, msg.FromSubTaskID)
	}
	if msg.Metadata["task_id"] != "task-routing" {
		t.Errorf("Metadata[task_id] = %q，期望 %q", msg.Metadata["task_id"], "task-routing")
	}

	// system_info 事件也必须携带真实目标，否则前端时间线画不出跨 agent 箭头。
	var found bool
	for _, ev := range evBus.events {
		if ev.Type != "system_info" {
			continue
		}
		if ev.Data["type"] != "agent_message_sent" {
			continue
		}
		found = true
		if ev.Data["to_agent"] != DefaultSupervisorAgentID {
			t.Errorf("system_info.to_agent = %v，期望 %q", ev.Data["to_agent"], DefaultSupervisorAgentID)
		}
		if ev.Data["to_sub_task_id"] != "root" {
			t.Errorf("system_info.to_sub_task_id = %v，期望 %q", ev.Data["to_sub_task_id"], "root")
		}
	}
	if !found {
		t.Fatalf("未发出 agent_message_sent 的 system_info 事件")
	}
}

// TestSendAgentMessageToRejectsEmptyTarget 验证目标为空的消息被拒绝发送，
// 不会进入 AgentBus 队列滞留（N0-01 的防御性守卫）。
func TestSendAgentMessageToRejectsEmptyTarget(t *testing.T) {
	engine, evBus, agBus := newRoutingTestEngine(t, EngineConfig{
		AgentID:   "agent_writer",
		SubTaskID: "root_agent_writer",
		Role:      AgentRoleWorker,
	})

	if ok := engine.sendAgentMessageTo("", "root", "observation", "无目标"); ok {
		t.Fatalf("空目标发送返回 true，期望 false")
	}
	if got := len(agBus.messages()); got != 0 {
		t.Fatalf("AgentBus 收到 %d 条空目标消息，期望 0 条", got)
	}
	for _, ev := range evBus.events {
		if ev.Type == "system_info" && ev.Data["type"] == "agent_message_sent" {
			t.Fatalf("空目标消息不应发出 agent_message_sent 事件")
		}
	}
}

// TestSendAgentMessageNoBusIsNoop 验证未启用 AgentBus 时发送是 no-op 且返回 false，
// 调用方（cmd/server 委托处理器）据此走自投递兜底通道。
func TestSendAgentMessageNoBusIsNoop(t *testing.T) {
	evBus := &recordingBus{}
	engine := NewEngine(EngineConfig{
		AgentID:   "agent_writer",
		SubTaskID: "root_agent_writer",
		Model:     "fake-model",
		Provider:  &fakeJudgeProvider{resp: "ack"},
		Contract:  harness.TaskContract{Goal: "routing test", Scope: "."},
	}, tool.NewRegistry(), evBus, "task-routing")

	if ok := engine.sendAgentMessage("leader", "observation", "无 bus"); ok {
		t.Fatalf("AgentBus 为 nil 时应返回 false")
	}
}

// deliveringAgentBus 是一个测试用功能型 AgentBus：它既记录所有经 SendMessage
// 发出的消息，又会在消息到达时同步投递给已注册的 (ToAgentID) handler，从而模拟
// 生产 orchestrator.AgentBus 的「发送即投递」语义，用于断言 Engine.SendAgentMessageTo
// 发出的消息确实能被目标 agent 的 handler 收到（N1-02 的「发闭环」端到端验证）。
type deliveringAgentBus struct {
	mu      sync.Mutex
	handlers map[string]func(AgentMessage)
	received []AgentMessage
}

func newDeliveringAgentBus() *deliveringAgentBus {
	return &deliveringAgentBus{handlers: map[string]func(AgentMessage){}}
}

func (b *deliveringAgentBus) RegisterHandler(agentID string, handler func(AgentMessage)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[agentID] = handler
}

func (b *deliveringAgentBus) RegisterHandlerBySubTask(agentID, subTaskID string, handler func(AgentMessage)) {
	b.RegisterHandler(agentID, handler)
}

func (b *deliveringAgentBus) UnregisterHandler(agentID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.handlers, agentID)
}

func (b *deliveringAgentBus) UnregisterHandlerBySubTask(agentID, subTaskID string) {
	b.UnregisterHandler(agentID)
}

func (b *deliveringAgentBus) SendMessage(msg AgentMessage) {
	b.mu.Lock()
	b.received = append(b.received, msg)
	h, ok := b.handlers[msg.ToAgentID]
	b.mu.Unlock()
	if ok {
		h(msg)
	}
}

// TestEngineSendAgentMessageToDeliversToHandler 是 N1-02 的核心端到端验证：
// Engine.SendAgentMessageTo（send_agent_message 工具的发送能力来源）发出的消息
// 必须被目标 agent 注册的 handler 收到，且携带正确的收发方身份与内容。
func TestEngineSendAgentMessageToDeliversToHandler(t *testing.T) {
	bus := newDeliveringAgentBus()
	var (
		got   AgentMessage
		gotMu sync.Mutex
	)
	bus.RegisterHandler("leader", func(m AgentMessage) {
		gotMu.Lock()
		got = m
		gotMu.Unlock()
	})

	engine := NewEngine(EngineConfig{
		AgentID:   "agent_writer",
		SubTaskID: "root_agent_writer",
		Model:     "fake-model",
		Provider:  &fakeJudgeProvider{resp: "ack"},
		Contract:  harness.TaskContract{Goal: "routing test", Scope: "."},
		AgentBus:  bus,
	}, tool.NewRegistry(), &recordingBus{}, "task-x")

	if ok := engine.SendAgentMessageTo("leader", "root", "request", "请审阅这段代码"); !ok {
		t.Fatalf("SendAgentMessageTo 返回 false，期望投递成功")
	}

	gotMu.Lock()
	defer gotMu.Unlock()
	if got.Content != "请审阅这段代码" {
		t.Errorf("handler 收到的 Content = %q", got.Content)
	}
	if got.FromAgentID != "agent_writer" || got.FromSubTaskID != "root_agent_writer" {
		t.Errorf("handler 收到的发送方身份错误: from=%q from_sub=%q", got.FromAgentID, got.FromSubTaskID)
	}
	if got.ToAgentID != "leader" || got.SubTaskID != "root" {
		t.Errorf("handler 收到的目标身份错误: to=%q sub=%q", got.ToAgentID, got.SubTaskID)
	}
	if got.Type != "request" {
		t.Errorf("handler 收到的 Type = %q，期望 request", got.Type)
	}
}

// TestEngineSendAgentMessageToNoBus 验证公开入口在未启用 AgentBus 时同样返回 false，
// 与私有 sendAgentMessageTo 行为一致（N1-02 注入点的 nil-safety 兜底）。
func TestEngineSendAgentMessageToNoBus(t *testing.T) {
	engine := NewEngine(EngineConfig{
		AgentID:   "agent_writer",
		SubTaskID: "root_agent_writer",
		Model:     "fake-model",
		Provider:  &fakeJudgeProvider{resp: "ack"},
		Contract:  harness.TaskContract{Goal: "routing test", Scope: "."},
	}, tool.NewRegistry(), &recordingBus{}, "task-x")

	if engine.SendAgentMessageTo("leader", "", "request", "无 bus") {
		t.Fatalf("AgentBus 为 nil 时 SendAgentMessageTo 应返回 false")
	}
}
