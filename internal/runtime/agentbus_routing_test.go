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
